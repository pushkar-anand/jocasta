package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// Topology asks every router, switch and access point what is connected to it,
// and records the answers.
//
// It is a separate task from Device, because it answers a different question:
// where a device is, not whether it is here. A switch that does not answer
// loses only its own branch of the tree.
type Topology struct {
	store    *inventory.Store
	interval time.Duration
	readers  []plugin.TopologyReader
	logger   *slog.Logger
}

// NewTopology builds the task that reads each of readers at each interval.
func NewTopology(
	log *slog.Logger,
	store *inventory.Store,
	interval time.Duration,
	readers ...plugin.TopologyReader,
) *Topology {
	if log == nil {
		log = slog.Default()
	}

	return &Topology{
		store:    store,
		interval: interval,
		readers:  readers,
		logger:   log,
	}
}

// Name returns the identifier for logs and scheduling.
func (t *Topology) Name() string { return "topology_reader" }

// Interval returns how often every source is read.
func (t *Topology) Interval() time.Duration { return t.interval }

// Run reads every source and records what each said. The error reports a
// source that fails. The task still records the others.
func (t *Topology) Run(ctx context.Context) error {
	var errs []error

	for _, r := range t.readers {
		if err := t.readAndSave(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// readAndSave reads one source and records the answer.
func (t *Topology) readAndSave(ctx context.Context, r plugin.TopologyReader) error {
	topo, err := r.Topology(ctx)
	if err != nil {
		return fmt.Errorf("read topology from %s: %w", r.Name(), err)
	}

	return SaveTopology(ctx, t.store, t.logger, r, topo)
}

// SaveTopology records what src said is connected to it, and logs what it
// recorded.
func SaveTopology(
	ctx context.Context,
	store *inventory.Store,
	log *slog.Logger,
	src plugin.TopologyReader,
	topo plugin.Topology,
) error {
	if err := store.RecordTopology(ctx, src.Name(), src.Kind(), topo); err != nil {
		return fmt.Errorf("record topology from %s: %w", src.Name(), err)
	}

	log.InfoContext(ctx, "recorded topology",
		slog.String("src", src.Name()),
		slog.Int("ports", len(topo.Ports)),
		slog.Int("seen", len(topo.Seen)),
		slog.Int("neighbours", len(topo.Neighbours)),
	)

	return nil
}

var _ task = (*Topology)(nil)
