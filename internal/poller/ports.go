package poller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// Ports probes the TCP ports of every address the inventory holds, on its own
// schedule.
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

// DueIn resumes the schedule across a restart: what is left of the interval
// since the last successful port scan finished, or nothing when that much has
// passed or none has ever succeeded.
//
// Keyed on the kind alone, like the device task: only this task writes PORTS
// scans, so nothing else can credit its schedule.
func (p *Ports) DueIn(ctx context.Context) time.Duration {
	return dueIn(ctx, p.store, dbtype.ScanPorts, p.interval, p.logger)
}

// Run scans every current address in the inventory and records what answered.
//
// With nothing to scan yet, because discovery has not run, it returns
// errNotReady, so the poller retries in a minute.
func (p *Ports) Run(ctx context.Context) error {
	targets, err := p.store.PortScanTargets(ctx)
	if err != nil {
		return fmt.Errorf("port scan targets: %w", err)
	}

	if len(targets) == 0 {
		p.logger.InfoContext(ctx, "no addresses to port-scan yet; waiting for discovery to populate the inventory")

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

var _ task = (*Ports)(nil)
