package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
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
// prompted it. The caller logs the error and moves on. The user's own answer
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
// A device folded or deleted between the ingest and here is skipped: the
// reading that touched it is what removed it.
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

	in, err := classifyInput(ctx, q, d)
	if err != nil {
		return fmt.Errorf("reclassify: %w", err)
	}

	got := classify.Device(in)

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
	// classifier going quiet while the device stays the same. Neither is an
	// event, the same call applyClaim makes for a first or retracted name.
	if prev == "" || got.Class == classify.Unknown || string(got.Class) == prev {
		return nil
	}

	detail := strings.Join(got.Reasons, "; ")

	return s.writeEvent(ctx, q, scanID, at, id, dbtype.EventDeviceClassified, prev, string(got.Class), detail)
}

// classifyInput gathers what the classifier reasons over for the stored device
// d: its vendor, name and hardware address kind, the ports a scan currently
// finds open, the services it advertises and the models they give, the
// addresses it holds now, and the name of a network one of them sits on.
//
// Reclassifying and explaining a guess both read the device through here, so
// an explanation is always the case the classifier itself was given.
func classifyInput(ctx context.Context, q *models.Queries, d *models.Device) (classify.Input, error) {
	ports, err := q.ListDeviceOpenPorts(ctx, d.ID)
	if err != nil {
		return classify.Input{}, fmt.Errorf("ports of device %d: %w", d.ID, err)
	}

	open := make([]uint16, 0, len(ports))
	for _, p := range ports {
		// device_ports.port is CHECK-constrained to 1-65535, so it fits.
		open = append(open, uint16(p.Port)) //nolint:gosec // range enforced by the column CHECK.
	}

	advertised, err := q.ListDeviceServices(ctx, d.ID)
	if err != nil {
		return classify.Input{}, fmt.Errorf("services of device %d: %w", d.ID, err)
	}

	var services, models []string

	for _, sv := range advertised {
		services = append(services, sv.Type)

		if sv.Model.Valid {
			models = append(models, sv.Model.String)
		}
	}

	names, err := q.DeviceNetworkNames(ctx, d.ID)
	if err != nil {
		return classify.Input{}, fmt.Errorf("networks of device %d: %w", d.ID, err)
	}

	network := ""
	if len(names) > 0 {
		network = names[0].String
	}

	addrs, err := q.ListDeviceAddresses(ctx, d.ID)
	if err != nil {
		return classify.Input{}, fmt.Errorf("addresses of device %d: %w", d.ID, err)
	}

	current := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if a.IsCurrent {
			current = append(current, a.IP.Addr)
		}
	}

	return classify.Input{
		Vendor:      d.Vendor.String,
		Hostname:    d.Hostname.String,
		Randomised:  d.IsRandomised,
		OpenPorts:   open,
		Services:    services,
		Models:      models,
		NetworkName: network,
		Addresses:   current,
	}, nil
}
