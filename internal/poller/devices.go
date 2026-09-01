package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

type Device struct {
	scanner  *scanner.Scanner
	store    *inventory.Store
	source   string
	interval time.Duration
	networks []*netip.Prefix
	logger   *slog.Logger
}

// NewDevice builds the task that sweeps networks and records what answered.
//
// source names the vantage point the sweep is taken from and is resolved by the
// caller: what a deployment calls itself is a wiring question, and the task
// stays testable without one.
func NewDevice(
	logger *slog.Logger,
	scanner *scanner.Scanner,
	store *inventory.Store,
	source string,
	interval time.Duration,
	networks []string,
) (*Device, error) {
	np := make([]*netip.Prefix, len(networks))

	for i, n := range networks {
		p, err := netip.ParsePrefix(n)
		if err != nil {
			return nil, fmt.Errorf("invalid network %q: %w", n, err)
		}

		np[i] = &p
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &Device{
		scanner:  scanner,
		store:    store,
		source:   source,
		interval: interval,
		networks: np,
		logger:   logger,
	}, nil
}

func (d *Device) Interval() time.Duration {
	return d.interval
}

func (d *Device) Name() string {
	return "device_scanner"
}

// DueIn resumes the sweep schedule across a restart: what is left of the
// interval since the last successful sweep finished, or nothing at all when
// that much has already passed or no sweep has ever succeeded.
//
// Its own kind, not the newest scan of any kind: a port scan says nothing about
// whether the devices are due to be looked at again.
//
// A store that cannot be read waits an interval rather than sweeping. Not
// knowing whether the work is due is a reason to hold off, and the alternative
// turns a restart loop into a scan loop -- which is the one failure the stored
// schedule exists to prevent.
func (d *Device) DueIn(ctx context.Context) time.Duration {
	at, err := d.store.LastSuccessfulScanAt(ctx, dbtype.ScanDiscovery)

	switch {
	case errors.Is(err, inventory.ErrNotFound):
		return 0
	case err != nil:
		d.logger.ErrorContext(ctx, "could not tell when the last sweep ran, holding off for one interval",
			logger.Err(err),
		)

		return d.interval
	}

	return d.interval - time.Since(at)
}

func (d *Device) Run(ctx context.Context) error {
	var errs []error

	for _, network := range d.networks {
		err := d.scanAndSaveNetwork(ctx, network)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (d *Device) scanAndSaveNetwork(ctx context.Context, network *netip.Prefix) error {
	result, err := d.scanner.Scan(ctx, *network)
	if err != nil {
		return fmt.Errorf("scan %s: %w", network, err)
	}

	saved, err := d.store.RecordSweep(ctx, d.source, *network, result)
	if err != nil {
		return fmt.Errorf("record sweep %s: %w", network, err)
	}

	d.logger.InfoContext(ctx,
		"recorded sweep",
		slog.Int64("scan", saved.ScanID),
		slog.Int("seen", saved.Seen),
		slog.Int("discovered", saved.Discovered),
		slog.Int("identified", saved.Identified),
		slog.Int("merged", saved.Merged),
	)

	return nil
}

var _ task = (*Device)(nil)
