package routeros

import (
	"context"
	"fmt"
	"log/slog"
)

// Verify reads /system/resource to prove the router is reachable, the REST
// service is enabled, and the credentials are accepted, returning what it
// learned about the router on the way.
//
// It is the one call worth making before trusting anything else here, and its
// error says which of the three failed: [ErrUnreachable] for a router that did
// not answer, [ErrUnauthorized] for one that refused the credentials.
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
