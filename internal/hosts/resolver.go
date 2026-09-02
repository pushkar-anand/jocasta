package hosts

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
)

// nameLookupTimeout bounds one reverse lookup. A LAN resolver answers in
// milliseconds, so a slow one is a resolver that has no record for the address.
const nameLookupTimeout = 500 * time.Millisecond

// resolver is the system resolver. It is a package variable, so tests can swap in
// one that answers from a stub instead of the network.
var resolver = net.DefaultResolver

// lookups collapses concurrent reverse lookups for one address into a single
// query. A sweep that merges the ARP and DHCP tables meets the same address in
// both, and the second sighting should not cost a second round trip.
//
// This deduplicates, it does not cache: a flight is held only while it is in
// progress, so a later sweep resolves again and sees current DNS.
var lookups singleflight.Group

// resolveName performs a reverse DNS PTR lookup for the given IP address.
// It applies a bounded timeout (nameLookupTimeout) derived from the parent context
// and strips any trailing root dot from the resulting domain name.
//
// Callers that arrive while a lookup for the same address is already running
// share its result, and so also share the deadline of whoever started it. A
// shared lookup cut short yields no name, which is how every other lookup
// failure reads here.
func resolveName(ctx context.Context, addr netip.Addr) string {
	key := addr.String()

	name, _, _ := lookups.Do(key, func() (any, error) {
		lookupCtx, cancel := context.WithTimeout(ctx, nameLookupTimeout)
		defer cancel()

		names, err := resolver.LookupAddr(lookupCtx, key)
		if err != nil || len(names) == 0 {
			return "", nil
		}

		return strings.TrimSuffix(names[0], "."), nil
	})

	resolved, _ := name.(string)

	return resolved
}
