// Package poller runs recurring network tasks such as device and port sweeps.
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
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// Device sweeps networks and asks every configured source which devices it
// knows about, recording both.
//
// One task and one schedule for both, because they answer the same question and
// are judged by the same online window. Two intervals would give the operator
// two knobs for "which devices are here now" and assemble the device list from
// readings taken at unrelated moments. A capability that answers a different
// question -- which port a device is plugged into -- gets its own task.
type Device struct {
	scanner     *scanner.Scanner
	store       *inventory.Store
	source      string
	interval    time.Duration
	networks    []*netip.Prefix
	discoverers []plugin.HostDiscoverer
	logger      *slog.Logger
}

// DeviceOption configures a Device.
type DeviceOption func(*Device)

// WithDiscoverers adds the sources asked for devices after each sweep.
func WithDiscoverers(ds ...plugin.HostDiscoverer) DeviceOption {
	return func(d *Device) {
		d.discoverers = append(d.discoverers, ds...)
	}
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
	opts ...DeviceOption,
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

	d := &Device{
		scanner:  scanner,
		store:    store,
		source:   source,
		interval: interval,
		networks: np,
		logger:   logger,
	}

	for _, opt := range opts {
		opt(d)
	}

	return d, nil
}

// Interval returns the polling period configured for this device task.
func (d *Device) Interval() time.Duration {
	return d.interval
}

// Name returns the identifier used in logs and scheduling.
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

// Run sweeps every configured network, then asks every source what it knows,
// and persists both.
//
// Sources are isolated from each other and from the sweep the same way the
// networks already are: an expired router credential contributes no facts and
// leaves everything else recorded.
func (d *Device) Run(ctx context.Context) error {
	var errs []error

	for _, network := range d.networks {
		err := d.scanAndSaveNetwork(ctx, network)
		if err != nil {
			errs = append(errs, err)
		}
	}

	for _, disc := range d.discoverers {
		err := d.discoverAndSave(ctx, disc)
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

// discoverAndSave asks one source what it knows and records the answer.
//
// Facts and an error together are a source half-read -- one table answering
// while another timed out -- and the facts that arrived are true, so they are
// recorded and the failure is logged rather than discarded. Only a read that
// returned nothing is a failure worth propagating.
func (d *Device) discoverAndSave(ctx context.Context, p plugin.HostDiscoverer) error {
	facts, err := p.Discover(ctx)

	switch {
	case err != nil && len(facts) == 0:
		return fmt.Errorf("discover %s: %w", p.Name(), err)
	case err != nil:
		d.logger.WarnContext(ctx, "source answered in part",
			slog.String("source", p.Name()),
			slog.Int("facts", len(facts)),
			logger.Err(err),
		)
	}

	saved, err := d.store.RecordFacts(ctx, p.Name(), p.Kind(), facts)
	if err != nil {
		return fmt.Errorf("record facts from %s: %w", p.Name(), err)
	}

	d.logger.InfoContext(ctx,
		"recorded source",
		slog.String("source", p.Name()),
		slog.Int64("scan", saved.ScanID),
		slog.Int("seen", saved.Seen),
		slog.Int("discovered", saved.Discovered),
		slog.Int("identified", saved.Identified),
		slog.Int("merged", saved.Merged),
		slog.Int("dropped", saved.Dropped),
	)

	return nil
}

var _ task = (*Device)(nil)
