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

// Ports probes the TCP ports of every address the inventory holds on a recorded
// network, on its own schedule.
//
// It is a task apart from Device because it answers a different question,
// what a device is running, and is judged by a different clock: the default
// is six hours against discovery's five minutes.
type Ports struct {
	scanner  *scanner.PortScanner
	store    *inventory.Store
	source   string
	interval time.Duration
	logger   *slog.Logger
}

// NewPorts builds the task that port-scans the inventory's current addresses.
//
// source names the vantage point the scan is taken from and is resolved by the
// caller, the same as the device task: it is the same host probing, so it files
// under the same source row the sweep does.
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

// Name returns the identifier used in logs and scheduling.
func (p *Ports) Name() string { return "port_scanner" }

// Interval returns the polling period configured for the port scan.
func (p *Ports) Interval() time.Duration { return p.interval }

// Run scans every current address on a recorded network and records what
// answered.
//
// With nothing to scan yet, because discovery has not run or has recorded no
// network, it returns errNotReady, so the poller retries in a minute.
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

// ScanNew scans the ports of devices a sweep has just discovered, at addrs,
// so they do not wait for the next full port scan. An address on no recorded
// network is left out, as the full scan leaves it out.
//
// It is not a run of the task: the full scan's schedule stays where it was.
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
