package hosts

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"time"
)

// nameLookupTimeout bounds one reverse lookup. A LAN resolver answers in
// milliseconds, so a slow one is a resolver that has no record for the address.
const nameLookupTimeout = 500 * time.Millisecond

// resolver is the system resolver. It is a package variable, so tests can swap in
// one that answers from a stub instead of the network.
var resolver = net.DefaultResolver

// resolveName performs a reverse DNS PTR lookup for the given IP address.
// It applies a bounded timeout (nameLookupTimeout) derived from the parent context
// and strips any trailing root dot from the resulting domain name.
func resolveName(ctx context.Context, addr netip.Addr) string {
	lookupCtx, cancel := context.WithTimeout(ctx, nameLookupTimeout)
	defer cancel()

	names, err := resolver.LookupAddr(lookupCtx, addr.String())
	if err != nil || len(names) == 0 {
		return ""
	}

	return strings.TrimSuffix(names[0], ".")
}
