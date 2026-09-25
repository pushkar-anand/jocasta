package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// Pruned counts what one prune deleted.
type Pruned struct {
	Events     int64
	Scans      int64
	Traffic    int64
	Attempts   int64
	Broadcasts int64
	Probes     int64
}

// Prune deletes every event and every finished scan older than retention, and
// every hourly traffic total and attempt count older than trafficRetention. A
// retention of zero keeps that kind forever.
//
// Events go first and in the same transaction, so a reader never sees an event
// whose scan has gone while the event stays. A scan's events are stamped at or
// after its start, so only a scan straddling the cutoff can leave a newer event
// behind, and that event keeps its row with scan_id set to null.
//
// A poller asking when its last successful scan ran loses the answer only when
// no scan of its kind succeeded within the window, and then running at once is
// what it would do anyway.
func (s *Store) Prune(ctx context.Context, retention, trafficRetention time.Duration) (*Pruned, error) {
	now := s.now()

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin prune: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)

	var res Pruned

	if retention > 0 {
		cutoff := dbtype.NewTime(now.Add(-retention))

		if res.Events, err = q.DeleteEventsBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune events: %w", err)
		}

		if res.Scans, err = q.DeleteScansBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune scans: %w", err)
		}
	}

	if trafficRetention > 0 {
		// Whole hours only: an hour is kept while any of it is inside the
		// window, so a view of the last N days never starts mid-hour.
		cutoff := dbtype.NewTime(now.Add(-trafficRetention).UTC().Truncate(time.Hour))

		if res.Traffic, err = q.DeleteTrafficBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune traffic: %w", err)
		}

		if res.Attempts, err = q.DeleteAttemptsBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune attempts: %w", err)
		}

		if res.Broadcasts, err = q.DeleteBroadcastsBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune broadcasts: %w", err)
		}

		if res.Probes, err = q.DeleteProbesBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune probes: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit prune: %w", err)
	}

	return &res, nil
}
