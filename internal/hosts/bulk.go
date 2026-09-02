package hosts

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"
)

// buildConcurrency caps active workers so a full /24 sweep does not
// burst hundreds of concurrent DNS queries or exhaust descriptors.
const buildConcurrency = 16

// BulkBuild concurrently builds and enriches multiple Host instances, capping
// concurrent builds at buildConcurrency.
//
// A malformed address or MAC costs its own entry and nothing else: the returned
// slice holds every host that built, in input order, and the error joins the
// failures, each naming the address it came from. Both can be non-empty at
// once, which is the ordinary result of sweeping a table a router filled in.
// Callers wanting all or nothing should check the error and drop the slice.
func BulkBuild(ctx context.Context, inputs []HostInput) ([]*Host, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	built := make([]*Host, len(inputs))
	errs := make([]error, len(inputs))

	var g errgroup.Group

	g.SetLimit(buildConcurrency)

	for i, input := range inputs {
		// Stop handing out work once the caller has given up. Builds already
		// running still finish, so what comes back stays coherent.
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

	// Failures are recorded per entry rather than returned, so that one bad row
	// neither cancels its siblings nor ends the group early. Wait therefore has
	// nothing left to report.
	_ = g.Wait()

	out := make([]*Host, 0, len(inputs))

	for _, h := range built {
		if h != nil {
			out = append(out, h)
		}
	}

	// A cancelled sweep is short, not complete. Saying so keeps a caller from
	// reading a truncated result as the whole table.
	return out, errors.Join(errors.Join(errs...), ctx.Err())
}
