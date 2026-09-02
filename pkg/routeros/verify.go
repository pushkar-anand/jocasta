package routeros

import (
	"context"
	"log/slog"
)

func (r *RouterOS) verifyConnection(ctx context.Context) error {
	res, err := r.get[map[string]any](ctx, resourceAPI)
	if err != nil {
		return err
	}

	r.logger.DebugContext(ctx, "connection verified", slog.Any("response", res))

	return nil
}
