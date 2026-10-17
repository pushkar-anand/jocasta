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

// nameLookupTimeout limits one reverse lookup. A LAN resolver answers in
// milliseconds. So a slow resolver has no record for the address.
const nameLookupTimeout = 500 * time.Millisecond

// resolver is the system resolver. It is a package variable. So tests can
// replace it with a resolver that answers from a stub.
var resolver = net.DefaultResolver

// lookups collapses reverse lookups for one address at the same time into one
// query. A sweep that merges the ARP and DHCP tables finds the same address in
// both. The second sighting must not cause a second round trip.
//
// Nothing is cached. A flight exists only while it runs. So a later sweep
// resolves again and sees the current DNS.
var lookups singleflight.Group

// resolveNames does the reverse lookup for one address, within
// nameLookupTimeout. It returns every name from the resolver, sorted, without
// the trailing root dot. An address can have several PTR records, such as the
// name of a server and the name of a role that it serves. The resolver returns
// them in no fixed order. resolveNames leaves out the names that
// systemd-resolved makes up. See synthesized.
//
// A caller can arrive while a lookup for the same address already runs. It
// then shares the result, and also the deadline of the first caller. If a
// shared lookup stops early, it gives no names. Every other lookup failure
// also gives no names here.
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

// synthesized are the names that systemd-resolved gives for addresses in the
// setup of its own host, such as _gateway for the default gateway. They tell
// what the address is to that host. They are not names that a person gave the
// device.
var synthesized = map[string]bool{
	"_gateway":       true,
	"_outbound":      true,
	"_localdnsstub":  true,
	"_localdnsproxy": true,
}

// resolveName returns the first reverse DNS name of addr, in sorted order. So
// an address with several names resolves to the same name on every lookup.
func resolveName(ctx context.Context, addr netip.Addr) string {
	if names := resolveNames(ctx, addr); len(names) > 0 {
		return names[0]
	}

	return ""
}

// ResolveName returns the reverse DNS name of addr. It returns the empty string
// when addr has none, or when the lookup does not answer in time.
func ResolveName(ctx context.Context, addr netip.Addr) string {
	return resolveName(ctx, addr)
}
