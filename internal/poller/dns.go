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

// DNSInterval is how often each resolver's query log is read. A minute keeps
// one read small on a busy network, and each read resumes from a stored
// cursor, so a longer gap costs only how fresh the counts are.
const DNSInterval = time.Minute

// DNS reads each resolver's query log from where the last read left off, and
// records the lookups against the devices that made them.
type DNS struct {
	store   *inventory.Store
	readers []plugin.QueryReader
	logger  *slog.Logger
}

// NewDNS builds the task that reads each of readers every [DNSInterval].
func NewDNS(log *slog.Logger, store *inventory.Store, readers ...plugin.QueryReader) *DNS {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &DNS{store: store, readers: readers, logger: log}
}

// Name returns the identifier used in logs and scheduling.
func (d *DNS) Name() string { return "dns_reader" }

// Interval returns how often every source is read.
func (d *DNS) Interval() time.Duration { return DNSInterval }

// Run reads every source and records what each logged. A source that fails is
// reported in the error and leaves the others recorded.
func (d *DNS) Run(ctx context.Context) error {
	var errs []error

	for _, r := range d.readers {
		if err := ReadQueries(ctx, d.store, d.logger, r); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// ReadQueries reads src from its stored cursor and records what it logged,
// with the cursor its read returned. A read that failed partway is recorded
// as far as it got, and logged; only a read that returned nothing is an
// error.
func ReadQueries(ctx context.Context, store *inventory.Store, log *slog.Logger, src plugin.QueryReader) error {
	cursor, err := store.QueryCursor(ctx, src.Name())
	if err != nil {
		return err
	}

	queries, next, err := src.Queries(ctx, cursor)
	if err != nil {
		return fmt.Errorf("read query log from %s: %w", src.Name(), err)
	}

	res, err := store.RecordQueries(ctx, src.Name(), src.Kind(), queries, next)
	if err != nil {
		return fmt.Errorf("record lookups from %s: %w", src.Name(), err)
	}

	log.DebugContext(ctx, "recorded lookups",
		slog.String("src", src.Name()),
		slog.Int("recorded", res.Recorded),
		slog.Int("dropped", res.Dropped),
	)

	return nil
}

var _ task = (*DNS)(nil)
