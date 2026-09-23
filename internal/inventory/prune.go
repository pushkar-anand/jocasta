package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// Pruned counts what one prune deleted.
type Pruned struct {
	Events int64
	Scans  int64
}

// Prune deletes every event and every finished scan older than retention.
//
// Events go first and in the same transaction, so a reader never sees an event
// whose scan has gone while the event stays. A scan's events are stamped at or
// after its start, so only a scan straddling the cutoff can leave a newer event
// behind, and that event keeps its row with scan_id set to null.
//
// A poller asking when its last successful scan ran loses the answer only when
// no scan of its kind succeeded within the window, and then running at once is
// what it would do anyway.
func (s *Store) Prune(ctx context.Context, retention time.Duration) (*Pruned, error) {
	cutoff := dbtype.NewTime(s.now().Add(-retention))

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin prune: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)

	events, err := q.DeleteEventsBefore(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("prune events: %w", err)
	}

	scans, err := q.DeleteScansBefore(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("prune scans: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit prune: %w", err)
	}

	return &Pruned{Events: events, Scans: scans}, nil
}
