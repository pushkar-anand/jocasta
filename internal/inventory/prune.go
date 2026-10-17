package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// Retention tells how long the store keeps each kind of record. A window of
// zero keeps that kind forever.
type Retention struct {
	// History limits the event log, the scan log, topology sightings, and the
	// services that a device stopped advertising.
	History time.Duration

	// Traffic limits hourly traffic totals, and attempt, broadcast and probe
	// counts.
	Traffic time.Duration

	// DNS limits hourly lookup counts, and the addresses in the answers to
	// lookups.
	DNS time.Duration

	// Devices limits how long the store keeps a device that its owner never
	// curated, after it was last seen. The store keeps a curated device
	// forever.
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
	Lookups    int64
	Answers    int64
	Sightings  int64
	Services   int64
	Devices    int64
}

// Prune deletes every record that is older than its window in r.
//
// Events go first, in the same transaction. So a reader never sees an event
// whose scan is gone. The events of a scan have a time at or after its start.
// So only a scan across the cutoff can leave a newer event. That event keeps
// its row, with scan_id set to null.
//
// A poller that asks when its last successful scan ran loses the answer only
// when no scan of its kind succeeded within the window. It would then run at
// once in any case.
//
// Prune deletes a device only when its owner gave it no label, notes, group or
// type, and did not ignore or watch it. A deleted device that a source sees
// again comes back as a new device.
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

		if res.Services, err = q.DeleteDeviceServicesBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune services: %w", err)
		}
	}

	if r.Traffic > 0 {
		// Whole hours only. An hour stays while a part of it is in the window.
		// So a view of the last N days never starts in the middle of an
		// hour.
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

	if r.DNS > 0 {
		// Whole hours only, as for traffic.
		cutoff := hourOf(now.Add(-r.DNS))

		if res.Lookups, err = q.DeleteDNSBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune lookups: %w", err)
		}

		if res.Answers, err = q.DeleteDNSAnswersBefore(ctx, cutoff); err != nil {
			return nil, fmt.Errorf("prune answers: %w", err)
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
