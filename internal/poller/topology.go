package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// Topology asks every router, switch and access point what is plugged into
// it, and records the answers.
//
// It is a task apart from Device because it answers a different question:
// where a device is, rather than whether it is here. A switch that will not
// answer costs only its own branch of the tree.
type Topology struct {
	store    *inventory.Store
	interval time.Duration
	readers  []plugin.TopologyReader
	logger   *slog.Logger
}

// NewTopology builds the task that reads each of readers every interval.
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

// Name returns the identifier used in logs and scheduling.
func (t *Topology) Name() string { return "topology_reader" }

// Interval returns how often every source is read.
func (t *Topology) Interval() time.Duration { return t.interval }

// DueIn resumes the schedule across a restart: what is left of the interval
// since any source was last read, or nothing when none has been.
func (t *Topology) DueIn(ctx context.Context) time.Duration {
	return resumeIn(ctx, t.interval, t.logger, t.store.LastTopologyReadAt)
}

// Run reads every source and records what each said. A source that fails is
// reported in the error and leaves the others recorded.
func (t *Topology) Run(ctx context.Context) error {
	var errs []error

	for _, r := range t.readers {
		if err := t.readAndSave(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// readAndSave reads one source and records the answer. A partial read is
// recorded and logged; only a read that returned nothing is an error.
func (t *Topology) readAndSave(ctx context.Context, r plugin.TopologyReader) error {
	topo, err := r.Topology(ctx)

	switch {
	case err != nil && topo.Empty():
		return fmt.Errorf("read topology from %s: %w", r.Name(), err)
	case err != nil:
		t.logger.WarnContext(ctx, "source described its ports in part",
			slog.String("src", r.Name()),
			logger.Err(err),
		)
	}

	return SaveTopology(ctx, t.store, t.logger, r, topo)
}

// SaveTopology records what src said is plugged into it and logs what was
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
