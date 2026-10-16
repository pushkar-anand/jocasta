package openwrt

import (
	"context"
	"fmt"
	"log/slog"
)

// Board is what `system board` tells about the router.
type Board struct {
	// Hostname is the name of the router, from System → Hostname.
	Hostname string `json:"hostname"`

	// Model is the hardware, such as "GL.iNet GL-MT6000". It is empty on a
	// virtual machine.
	Model string `json:"model"`

	Release struct {
		// Version is the OpenWrt release, such as "24.10.4".
		Version string `json:"version"`

		// Description is the release with its build, such as
		// "OpenWrt 24.10.4 r28959-29397011cc".
		Description string `json:"description"`
	} `json:"release"`
}

// Board reads what the router tells about itself.
func (o *OpenWrt) Board(ctx context.Context) (*Board, error) {
	return call[Board](ctx, o, "system", "board", nil)
}

// Verify reads the board. This proves that the router is reachable, it serves
// ubus, and it accepts the login. Verify returns what it learned.
//
// Make this call before you trust other calls in this package. Its error tells
// which check failed. [ErrUnreachable] is a router that did not answer.
// [ErrNotFound] is a router that does not serve ubus. [ErrUnauthorized] is a
// router that refused the login.
func (o *OpenWrt) Verify(ctx context.Context) (*Board, error) {
	b, err := o.Board(ctx)
	if err != nil {
		return nil, fmt.Errorf("verify %s: %w", o.Addr(), err)
	}

	o.logger.DebugContext(ctx, "openwrt connection verified",
		slog.String("addr", o.Addr()),
		slog.String("hostname", b.Hostname),
		slog.String("version", b.Release.Version),
	)

	return b, nil
}
