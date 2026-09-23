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

// Prune deletes events and scans that have aged past the retention window.
type Prune struct {
	store     *inventory.Store
	retention time.Duration
	logger    *slog.Logger
}

// NewPrune builds the task that keeps the event and scan logs to retention.
func NewPrune(log *slog.Logger, store *inventory.Store, retention time.Duration) *Prune {
	if log == nil {
		log = slog.Default()
	}

	return &Prune{
		store:     store,
		retention: retention,
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

	p.logger.InfoContext(ctx, "pruned events and scans past retention",
		slog.Duration("retention", p.retention),
		slog.Int64("events", res.Events),
		slog.Int64("scans", res.Scans),
	)

	return nil
}

var _ task = (*Prune)(nil)
