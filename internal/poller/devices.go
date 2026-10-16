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
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// Device sweeps networks, and asks every configured source which devices it
// knows. It records both.
//
// Both share one task and one schedule, because they answer the same question
// and use the same online window. Two intervals would give the operator two
// settings for "which devices are here now". The device list would then come
// from readings at unrelated times. A capability that answers a different
// question gets its own task, such as the port that a device connects to.
type Device struct {
	scanner     *scanner.Scanner
	store       *inventory.Store
	source      string
	interval    time.Duration
	networks    []*netip.Prefix
	discoverers []plugin.HostDiscoverer
	ports       *Ports
	logger      *slog.Logger
}

// DeviceOption configures a Device.
type DeviceOption func(*Device)

// WithDiscoverers adds the sources that the task asks for devices after each
// sweep.
func WithDiscoverers(ds ...plugin.HostDiscoverer) DeviceOption {
	return func(d *Device) {
		d.discoverers = append(d.discoverers, ds...)
	}
}

// WithPortScan makes each run port-scan the devices that it found, through the
// port-scan task. Without it, they wait for the next full scan.
func WithPortScan(p *Ports) DeviceOption {
	return func(d *Device) {
		d.ports = p
	}
}

// NewDevice builds the task that sweeps networks and records what answered.
//
// source names the place from which the sweep runs. The caller resolves it.
// The name of a deployment is a question for the wiring code. So the task
// stays testable without a name.
func NewDevice(
	log *slog.Logger,
	sc *scanner.Scanner,
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

	if log == nil {
		log = slog.Default()
	}

	d := &Device{
		scanner:  sc,
		store:    store,
		source:   source,
		interval: interval,
		networks: np,
		logger:   log,
	}

	for _, opt := range opts {
		opt(d)
	}

	return d, nil
}

// Interval returns the configured polling period of this device task.
func (d *Device) Interval() time.Duration {
	return d.interval
}

// Name returns the identifier for logs and scheduling.
func (d *Device) Name() string {
	return "device_scanner"
}

// Run sweeps every configured network, then asks every source what it knows.
// It stores both.
//
// Sources are separate from each other and from the sweep, as the networks
// are. An expired router credential gives no facts, but everything else is
// still recorded.
func (d *Device) Run(ctx context.Context) error {
	var (
		errs  []error
		fresh []netip.Addr
	)

	for _, network := range d.networks {
		found, err := d.scanAndSaveNetwork(ctx, network)
		if err != nil {
			errs = append(errs, err)
		}

		fresh = append(fresh, found...)
	}

	for _, disc := range d.discoverers {
		found, err := d.discoverAndSave(ctx, disc)
		if err != nil {
			errs = append(errs, err)
		}

		fresh = append(fresh, found...)
	}

	// The next full port scan also covers them. So a failure here is not a
	// failure of the run.
	if d.ports != nil && len(fresh) > 0 {
		if err := d.ports.ScanNew(ctx, fresh); err != nil {
			d.logger.WarnContext(ctx, "could not port-scan the new devices; the next full port scan will", logger.Err(err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// scanAndSaveNetwork sweeps network, records the result, and returns the
// addresses of the devices that it found. A failed sweep returns, and records
// no successful inventory update.
func (d *Device) scanAndSaveNetwork(ctx context.Context, network *netip.Prefix) ([]netip.Addr, error) {
	result, err := d.scanner.Scan(ctx, *network)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", network, err)
	}

	saved, err := d.store.RecordSweep(ctx, d.source, *network, result)
	if err != nil {
		return nil, fmt.Errorf("record sweep %s: %w", network, err)
	}

	d.logger.InfoContext(ctx, "recorded sweep", resultArgs(saved)...)

	return saved.New, nil
}

// discoverAndSave asks one source what it knows, records the answer, and
// returns the addresses of the devices that it found.
//
// If the source returns an error, discoverAndSave records nothing from it and
// returns the error.
func (d *Device) discoverAndSave(ctx context.Context, p plugin.HostDiscoverer) ([]netip.Addr, error) {
	// Segments come before devices. Ingest matches each address to a recorded
	// network. So a VLAN that is not in the sweep list must exist before its
	// facts arrive.
	if np, ok := p.(plugin.NetworkDiscoverer); ok {
		d.recordNetworks(ctx, np)
	}

	facts, err := p.Discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover %s: %w", p.Name(), err)
	}

	saved, err := d.store.RecordFacts(ctx, p.Name(), p.Kind(), facts)
	if err != nil {
		return nil, fmt.Errorf("record facts from %s: %w", p.Name(), err)
	}

	d.logger.With(slog.String("src", p.Name())).
		InfoContext(ctx, "recorded source", resultArgs(saved)...)

	return saved.New, nil
}

// recordNetworks learns the names that the source gives the segments that it
// serves.
//
// A failure loses only the names of the segments. So recordNetworks only logs
// it. A router that does not list its addresses can still tell which devices
// are on them.
func (d *Device) recordNetworks(ctx context.Context, p plugin.NetworkDiscoverer) {
	nets, err := p.Networks(ctx)
	if err != nil {
		d.logger.WarnContext(ctx, "source did not say which segments it serves",
			slog.String("src", p.Name()),
			logger.Err(err),
		)

		return
	}

	if err := d.store.RecordNetworks(ctx, nets); err != nil {
		d.logger.ErrorContext(ctx, "could not record the segments a source serves",
			slog.String("src", p.Name()),
			logger.Err(err),
		)

		return
	}

	d.logger.InfoContext(ctx, "recorded segments",
		slog.String("src", p.Name()),
		slog.Int("networks", len(nets)),
	)
}

// resultArgs returns the counts in result as log arguments.
func resultArgs(result *inventory.Result) []any {
	return []any{
		slog.Int64("scan", result.ScanID),
		slog.Int("seen", result.Seen),
		slog.Int("discovered", result.Discovered),
		slog.Int("identified", result.Identified),
		slog.Int("merged", result.Merged),
		slog.Int("released", result.Released),
		slog.Int("dropped", result.Dropped),
		slog.Int("back", result.Back),
		slog.Int("quiet", result.Quiet),
	}
}

var _ task = (*Device)(nil)
