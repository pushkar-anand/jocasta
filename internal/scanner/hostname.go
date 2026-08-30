package scanner

import (
	"context"
	"strings"
	"sync"
	"time"
)

// nameLookupTimeout bounds one reverse lookup. A LAN resolver answers in
// milliseconds, so a slow one is a resolver that has no record for the address.
const nameLookupTimeout = 500 * time.Millisecond

// nameLookupConcurrency caps in-flight lookups so a sweep of a full /24 does not
// arrive at the DNS server as a single burst of hundreds of queries.
const nameLookupConcurrency = 16

// resolveNames fills in the hostname for each host from reverse DNS, in place.
// A host with no PTR record keeps an empty name; that is common and expected.
func resolveNames(ctx context.Context, hosts []Host) {
	sem := make(chan struct{}, nameLookupConcurrency)

	var wg sync.WaitGroup

	for i := range hosts {
		wg.Go(func() {
			h := &hosts[i]

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			lookupCtx, cancel := context.WithTimeout(ctx, nameLookupTimeout)
			defer cancel()

			names, err := resolver.LookupAddr(lookupCtx, h.Addr.String())
			if err != nil || len(names) == 0 {
				return
			}

			// PTR records are returned fully qualified with a trailing dot, which
			// is correct on the wire and noise in a UI.
			h.Hostname = strings.TrimSuffix(names[0], ".")
		})
	}

	wg.Wait()
}
