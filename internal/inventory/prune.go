package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// Retention says how long each kind of record is kept. A window of zero keeps
// that kind forever.
type Retention struct {
	// History bounds the event log, the scan log and topology sightings.
	History time.Duration

	// Traffic bounds hourly traffic totals and attempt, broadcast and probe
	// counts.
	Traffic time.Duration

	// Devices bounds how long a device its owner never curated is kept after
	// it was last seen. A curated device is kept forever.
	Devices time.Duration
}

// Pruned counts what one prune deleted.
type Pruned struct {
	Events     int64
	Scans      int64
	Traffic    int64
	Attempts   int64
	Broadcasts int64
	Probes     int64
	Sightings  int64
	Devices    int64
}

// Prune deletes every record older than its window in r.
//
// Events go first and in the same transaction, so a reader never sees an event
// whose scan has gone while the event stays. A scan's events are stamped at or
// after its start, so only a scan straddling the cutoff can leave a newer event
// behind, and that event keeps its row with scan_id set to null.
//
// A poller asking when its last successful scan ran loses the answer only when
// no scan of its kind succeeded within the window, and then running at once is
// what it would do anyway.
//
// A device is deleted only when its owner gave it no label, notes, group or
// type and did not mark it ignored. A deleted device seen again comes back as
// a new one.
func (s *Store) Prune(ctx context.Context, r Retention) (*Pruned, error) {
	now := s.now()

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin prune: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)

	var res Pruned

	if r.History > 0 {
		cutoff := dbtype.NewTime(now.Add(-r.History))

		if res.Events, err = q.DeleteEventsBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune events: %w", err)
		}

		if res.Scans, err = q.DeleteScansBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune scans: %w", err)
		}

		if res.Sightings, err = q.DeleteTopologySightingsBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune sightings: %w", err)
		}
	}

	if r.Traffic > 0 {
		// Whole hours only: an hour is kept while any of it is inside the
		// window, so a view of the last N days never starts mid-hour.
		cutoff := hourOf(now.Add(-r.Traffic))

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

	if r.Devices > 0 {
		cutoff := dbtype.NewTime(now.Add(-r.Devices))

		if res.Devices, err = q.DeleteUncuratedDevicesBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune devices: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit prune: %w", err)
	}

	return &res, nil
}
