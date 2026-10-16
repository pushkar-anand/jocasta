package routeros

import (
	"context"
	"fmt"
	"log/slog"
)

// Verify reads /system/resource. This proves that the router is reachable,
// the REST service is enabled, and the router accepts the credentials. Verify
// returns what it learned about the router.
//
// Make this call before you trust other calls in this package. Its error tells
// which of the three checks failed. [ErrUnreachable] is a router that did not
// answer. [ErrUnauthorized] is a router that refused the credentials.
func (r *RouterOS) Verify(ctx context.Context) (*Resource, error) {
	res, err := r.Resource(ctx)
	if err != nil {
		return nil, fmt.Errorf("verify %s: %w", r.Addr(), err)
	}

	r.logger.DebugContext(ctx, "routeros connection verified",
		slog.String("addr", r.Addr()),
		slog.String("board", res.BoardName),
		slog.String("version", res.Version),
	)

	return res, nil
}
