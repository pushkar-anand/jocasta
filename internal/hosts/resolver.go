package hosts

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
)

// nameLookupTimeout bounds one reverse lookup. A LAN resolver answers in
// milliseconds, so a slow one is a resolver that has no record for the address.
const nameLookupTimeout = 500 * time.Millisecond

// resolver is the system resolver. It is a package variable, so tests can swap in
// one that answers from a stub.
var resolver = net.DefaultResolver

// lookups collapses concurrent reverse lookups for one address into a single
// query. A sweep that merges the ARP and DHCP tables meets the same address in
// both, and the second sighting should not cost a second round trip.
//
// Nothing is cached: a flight is held only while it is in progress, so a later
// sweep resolves again and sees current DNS.
var lookups singleflight.Group

// resolveNames does the reverse lookup for one address, bounded by
// nameLookupTimeout, and returns every name the resolver gave, sorted, with the
// trailing root dot trimmed. An address can carry several PTR records, such as
// a server's own name beside the name of a role it serves, and the resolver
// returns them in no fixed order. The names systemd-resolved makes up are left
// out (see synthesized).
//
// Callers that arrive while a lookup for the same address is already running
// share its result, and so also share the deadline of whoever started it. A
// shared lookup cut short yields no names, which is how every other lookup
// failure reads here.
func resolveNames(ctx context.Context, addr netip.Addr) []string {
	key := addr.String()

	result, _, _ := lookups.Do(key, func() (any, error) {
		lookupCtx, cancel := context.WithTimeout(ctx, nameLookupTimeout)
		defer cancel()

		names, err := resolver.LookupAddr(lookupCtx, key)
		if err != nil {
			return []string(nil), nil
		}

		out := make([]string, 0, len(names))
		for _, n := range names {
			if n = strings.TrimSuffix(n, "."); !synthesized[n] {
				out = append(out, n)
			}
		}

		slices.Sort(out)

		return slices.Compact(out), nil
	})

	names, _ := result.([]string)

	return names
}

// synthesized are the names systemd-resolved answers with for addresses in its
// own host's setup, such as _gateway for the default gateway. They say what the
// address is to that host, not what anyone named the device.
var synthesized = map[string]bool{
	"_gateway":       true,
	"_outbound":      true,
	"_localdnsstub":  true,
	"_localdnsproxy": true,
}

// resolveName returns the first of addr's reverse DNS names in sorted order, so
// an address with several resolves to the same one on every lookup.
func resolveName(ctx context.Context, addr netip.Addr) string {
	if names := resolveNames(ctx, addr); len(names) > 0 {
		return names[0]
	}

	return ""
}

// ResolveName returns the reverse DNS name for addr, or the empty string when
// it has none or the lookup does not answer within a lookup's bound.
func ResolveName(ctx context.Context, addr netip.Addr) string {
	return resolveName(ctx, addr)
}
