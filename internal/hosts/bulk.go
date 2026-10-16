package hosts

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"
)

// buildConcurrency limits the active workers. So a full /24 sweep does not
// send hundreds of DNS queries at once, or use up all descriptors.
const buildConcurrency = 16

// BulkBuild builds and enriches several Host instances at the same time. It
// runs at most buildConcurrency builds at once.
//
// A malformed address or MAC loses only its own entry. The returned slice has
// every host that built, in input order. The error joins the failures, and
// each failure names its address. Both can be non-empty at once. This is the
// normal result of a sweep of a table that a router filled. A caller that
// wants all or nothing must check the error and discard the slice.
func BulkBuild(ctx context.Context, inputs []HostInput) ([]*Host, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	built := make([]*Host, len(inputs))
	errs := make([]error, len(inputs))

	var g errgroup.Group

	g.SetLimit(buildConcurrency)

	for i, input := range inputs {
		// Stop giving out work when the caller stops. Builds that already run
		// still finish. So the result stays consistent.
		if ctx.Err() != nil {
			break
		}

		g.Go(func() error {
			h, err := BuildHost(ctx, input)
			if err != nil {
				errs[i] = fmt.Errorf("host %q: %w", input.IP, err)

				return nil
			}

			built[i] = h

			return nil
		})
	}

	// Each worker records its failure on its entry and returns nil. So one bad
	// row does not cancel the other workers, or end the group early. So Wait
	// has nothing to report.
	_ = g.Wait()

	out := make([]*Host, 0, len(inputs))

	for _, h := range built {
		if h != nil {
			out = append(out, h)
		}
	}

	// A cancelled sweep is incomplete. This error stops a caller from reading
	// a cut result as the whole table.
	return out, errors.Join(errors.Join(errs...), ctx.Err())
}
