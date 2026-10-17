package poller

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"time"

	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// Ports probes the TCP ports of every address of the inventory on a recorded
// network, on its own schedule.
//
// It is a separate task from Device, because it answers a different question:
// what a device runs. It also has a different clock. The default is six
// hours, but discovery runs every five minutes.
type Ports struct {
	scanner  *scanner.PortScanner
	store    *inventory.Store
	source   string
	interval time.Duration
	logger   *slog.Logger
}

// NewPorts builds the task that port-scans the current addresses of the
// inventory.
//
// source names the place from which the scan runs. The caller resolves it, as
// for the device task. The same host probes. So the scan uses the same source
// row as the sweep.
func NewPorts(
	log *slog.Logger,
	sc *scanner.PortScanner,
	store *inventory.Store,
	source string,
	interval time.Duration,
) *Ports {
	if log == nil {
		log = slog.Default()
	}

	return &Ports{
		scanner:  sc,
		store:    store,
		source:   source,
		interval: interval,
		logger:   log,
	}
}

// Name returns the identifier for logs and scheduling.
func (p *Ports) Name() string { return "port_scanner" }

// Interval returns the configured polling period of the port scan.
func (p *Ports) Interval() time.Duration { return p.interval }

// Run scans every current address on a recorded network and records what
// answered.
//
// If there is nothing to scan yet, it returns errNotReady. This happens when
// discovery did not run, or recorded no network. The poller then tries again
// in a minute.
func (p *Ports) Run(ctx context.Context) error {
	targets, err := p.store.PortScanTargets(ctx)
	if err != nil {
		return fmt.Errorf("port scan targets: %w", err)
	}

	if len(targets) == 0 {
		p.logger.InfoContext(ctx, "no address on a recorded network to port-scan yet; waiting for discovery to record one")

		return errNotReady
	}

	results := p.scanner.Scan(ctx, targets, time.Now())

	sum, err := p.store.RecordPorts(ctx, p.source, results)
	if err != nil {
		return fmt.Errorf("record port scan: %w", err)
	}

	p.logger.InfoContext(ctx, "recorded port scan",
		slog.Int64("scan", sum.ScanID),
		slog.Int("devices", sum.Devices),
		slog.Int("dropped", sum.Dropped),
		slog.Int("open", sum.Open),
		slog.Int("opened", sum.Opened),
		slog.Int("closed", sum.Closed),
	)

	return nil
}

// ScanNew scans the ports of devices that a sweep just found, at addrs. So they
// do not wait for the next full port scan. It leaves out an address on no
// recorded network, as the full scan does.
//
// It is not a run of the task. The schedule of the full scan does not
// change.
func (p *Ports) ScanNew(ctx context.Context, addrs []netip.Addr) error {
	all, err := p.store.PortScanTargets(ctx)
	if err != nil {
		return fmt.Errorf("port scan targets: %w", err)
	}

	targets := slices.DeleteFunc(all, func(t scanner.PortTarget) bool {
		return !slices.Contains(addrs, t.Addr)
	})

	if len(targets) == 0 {
		return nil
	}

	results := p.scanner.Scan(ctx, targets, time.Now())

	sum, err := p.store.RecordNewDevicePorts(ctx, p.source, results)
	if err != nil {
		return fmt.Errorf("record port scan of new devices: %w", err)
	}

	p.logger.InfoContext(ctx, "recorded port scan of new devices",
		slog.Int64("scan", sum.ScanID),
		slog.Int("devices", sum.Devices),
		slog.Int("open", sum.Open),
	)

	return nil
}

var _ task = (*Ports)(nil)
