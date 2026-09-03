package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// reclassify re-runs the device-type classifier over the devices a reading
// touched and records the guess where it moved.
//
// It runs after the ingest it follows has committed, in its own transaction: a
// guess is advisory, so a failure here must not roll back the scan that
// prompted it -- the caller logs the error and moves on. The user's own answer
// in device_type is never read or written here; only device_class is.
func (s *Store) reclassify(ctx context.Context, scanID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reclassify: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)
	at := s.stamp()

	for _, id := range ids {
		if err := s.classifyOne(ctx, q, scanID, at, id); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reclassify: %w", err)
	}

	return nil
}

// classifyOne reads what the inventory knows about one device, runs the
// classifier, and writes the guess back when it changed.
//
// A device folded or deleted between the ingest and here is skipped rather than
// treated as an error: the reading that touched it is what removed it.
func (s *Store) classifyOne(
	ctx context.Context,
	q *models.Queries,
	scanID int64,
	at dbtype.Time,
	id int64,
) error {
	d, err := q.GetDevice(ctx, id)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("reclassify: device %d: %w", id, err)
	}

	ports, err := q.ListDeviceOpenPorts(ctx, id)
	if err != nil {
		return fmt.Errorf("reclassify: ports of device %d: %w", id, err)
	}

	open := make([]uint16, 0, len(ports))
	for _, p := range ports {
		// device_ports.port is CHECK-constrained to 1-65535, so it fits.
		open = append(open, uint16(p.Port)) //nolint:gosec // range enforced by the column CHECK.
	}

	names, err := q.DeviceNetworkNames(ctx, id)
	if err != nil {
		return fmt.Errorf("reclassify: networks of device %d: %w", id, err)
	}

	network := ""
	if len(names) > 0 {
		network = names[0].String
	}

	got := classify.Device(classify.Input{
		Vendor:      d.Vendor.String,
		Hostname:    d.Hostname.String,
		Randomised:  d.IsRandomised,
		OpenPorts:   open,
		NetworkName: network,
	})

	prev := d.DeviceClass.String
	if string(got.Class) == prev && string(got.Confidence) == d.DeviceClassConfidence.String {
		return nil
	}

	err = q.SetDeviceClass(ctx, models.SetDeviceClassParams{
		DeviceClass:           nullString(string(got.Class)),
		DeviceClassConfidence: nullString(string(got.Confidence)),
		ID:                    id,
	})
	if err != nil {
		return fmt.Errorf("reclassify: set class of device %d: %w", id, err)
	}

	// The log records a guess moving between two settled classes. A first guess
	// is part of discovering the device, and a guess lapsing to nothing is the
	// classifier going quiet, not the device changing -- neither is an event,
	// the same call applyClaim makes for a first or retracted name.
	if prev == "" || got.Class == classify.Unknown || string(got.Class) == prev {
		return nil
	}

	detail := strings.Join(got.Reasons, "; ")

	return s.writeEvent(ctx, q, scanID, at, id, dbtype.EventDeviceClassified, prev, string(got.Class), detail)
}
