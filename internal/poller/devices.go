package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
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

func (d *Device) DueIn(ctx context.Context) time.Duration {
	lastScanTime, err := d.store.LastSuccessfulDeviceScan(ctx)
	if err != nil {
		d.logger.ErrorContext(ctx, "failed to get last successful device scan time", logger.Err(err))
		return d.interval
	}

	if lastScanTime.IsZero() {
		return d.interval
	}

	diff := time.Since(lastScanTime)

	if diff > d.interval {
		return time.Duration(0)
	}

	return diff
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
