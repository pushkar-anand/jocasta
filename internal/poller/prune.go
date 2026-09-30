package poller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// pruneInterval is how often the prune runs. The delete is an indexed range
// scan, so running hourly costs little and keeps the logs within an hour of
// the window.
const pruneInterval = time.Hour

// Prune deletes the records and uncurated devices that have aged past their
// retention windows.
type Prune struct {
	store     *inventory.Store
	retention inventory.Retention
	logger    *slog.Logger
}

// NewPrune builds the task that keeps the inventory to r.
func NewPrune(log *slog.Logger, store *inventory.Store, r inventory.Retention) *Prune {
	if log == nil {
		log = slog.Default()
	}

	return &Prune{
		store:     store,
		retention: r,
		logger:    log,
	}
}

// Name returns the identifier used in logs and scheduling.
func (p *Prune) Name() string { return "pruner" }

// Interval returns how often the prune runs.
func (p *Prune) Interval() time.Duration { return pruneInterval }

// DueIn is always now: a prune has no schedule worth resuming, and running one
// early deletes nothing a later one would have kept.
func (p *Prune) DueIn(context.Context) time.Duration { return 0 }

// Run deletes whatever has aged past the retention window.
func (p *Prune) Run(ctx context.Context) error {
	res, err := p.store.Prune(ctx, p.retention)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}

	p.logger.InfoContext(ctx, "pruned records past retention",
		slog.Duration("retention", p.retention.History),
		slog.Duration("traffic_retention", p.retention.Traffic),
		slog.Duration("device_retention", p.retention.Devices),
		slog.Int64("events", res.Events),
		slog.Int64("scans", res.Scans),
		slog.Int64("traffic", res.Traffic),
		slog.Int64("attempts", res.Attempts),
		slog.Int64("broadcasts", res.Broadcasts),
		slog.Int64("probes", res.Probes),
		slog.Int64("sightings", res.Sightings),
		slog.Int64("devices", res.Devices),
	)

	return nil
}

var _ task = (*Prune)(nil)
