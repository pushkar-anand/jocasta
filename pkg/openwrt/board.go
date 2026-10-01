package openwrt

import (
	"context"
	"fmt"
	"log/slog"
)

// Board is what `system board` says about the router.
type Board struct {
	// Hostname is the router's own name, from System → Hostname.
	Hostname string `json:"hostname"`

	// Model is the hardware, such as "GL.iNet GL-MT6000", and is empty on a
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

// Board reads what the router says about itself.
func (o *OpenWrt) Board(ctx context.Context) (*Board, error) {
	return call[Board](ctx, o, "system", "board", nil)
}

// Verify reads the board to prove the router is reachable, ubus is served, and
// the login is accepted, returning what it learned on the way.
//
// It is the one call worth making before trusting anything else here, and its
// error says which failed: [ErrUnreachable] for a router that did not answer,
// [ErrNotFound] for one that does not serve ubus, [ErrUnauthorized] for one
// that refused the login.
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
