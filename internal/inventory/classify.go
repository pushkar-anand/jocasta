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

// reclassify runs the device-type classifier again over the devices that a
// reading changed. It records the guess where the guess changed.
//
// It runs after its ingest committed, in its own transaction. A guess is only
// advice. So a failure here must not roll back the scan. The caller logs the
// error and continues. This code never reads or writes the answer of the user
// in device_type. It uses only device_class.
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

// classifyOne reads what the inventory knows about one device, and runs the
// classifier. It writes the guess back when the guess changed.
//
// It skips a device that was merged or deleted between the ingest and here.
// The reading that changed it removed it.
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

	// The log records a guess that changes between two settled classes. A
	// first guess is part of the discovery of the device. A guess that changes
	// to nothing means that the classifier is silent, but the device is the
	// same. Neither is an event. applyClaim decides in the same way for a
	// first or removed name.
	if prev == "" || got.Class == classify.Unknown || string(got.Class) == prev {
		return nil
	}

	detail := strings.Join(got.Reasons, "; ")

	return s.writeEvent(ctx, q, scanID, at, id, dbtype.EventDeviceClassified, prev, string(got.Class), detail)
}

// classifyInput collects the data of the classifier for the stored device d:
//   - its vendor, name and hardware address kind
//   - the ports that a scan finds open now
//   - the services that it advertises, and their models
//   - its current addresses, and the name of the network of one of them
//
// A new classification and an explanation both read the device through here.
// So an explanation always shows the case that the classifier itself got.
func classifyInput(ctx context.Context, q *models.Queries, d *models.Device) (classify.Input, error) {
	ports, err := q.ListDeviceOpenPorts(ctx, d.ID)
	if err != nil {
		return classify.Input{}, fmt.Errorf("ports of device %d: %w", d.ID, err)
	}

	open := make([]uint16, 0, len(ports))
	for _, p := range ports {
		// A CHECK limits device_ports.port to 1-65535, so it fits.
		open = append(open, uint16(p.Port)) //nolint:gosec // range enforced by the column CHECK.
	}

	advertised, err := q.ListDeviceServicesInWindow(ctx, d.ID)
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
		Vendor:      d.VendorShort.String,
		Hostname:    d.Hostname.String,
		Randomised:  d.IsRandomised,
		OpenPorts:   open,
		Services:    services,
		Models:      models,
		NetworkName: network,
		Addresses:   current,
	}, nil
}
