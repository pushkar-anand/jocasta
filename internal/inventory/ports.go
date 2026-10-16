package inventory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// PortChangeWindow is how far back the overview counts a port as newly opened
// or closed. It is a rolling day, as the other "today" count of the
// overview.
const PortChangeWindow = 24 * time.Hour

// PortSummary counts what the record of one port scan changed.
type PortSummary struct {
	ScanID int64

	// Targets is how many addresses the scan reported on. Devices is how many
	// devices of the inventory those addresses resolved to. Dropped is how
	// many addresses resolved to no device. These addresses retired between
	// the sweep that found them and this scan.
	Targets int
	Devices int
	Dropped int

	// Open is how many open ports the scan saw across those devices. Opened
	// and Closed count only the changes: a port that newly answers, and a port
	// that was open and stopped answering.
	Open   int
	Opened int
	Closed int
}

// PortOverview returns current open-service counts and recent changes. It
// leaves out ignored devices, as the device list of the overview does.
func (s *Store) PortOverview(ctx context.Context, serviceLimit int) (*PortOverview, error) {
	stats, err := s.q.PortStats(ctx, dbtype.NewTime(s.now().Add(-PortChangeWindow)))
	if err != nil {
		return nil, fmt.Errorf("port stats: %w", err)
	}

	rows, err := s.q.CommonOpenServices(ctx, int64(serviceLimit))
	if err != nil {
		return nil, fmt.Errorf("common open services: %w", err)
	}

	services := make([]*ServiceCount, 0, len(rows))
	for _, row := range rows {
		services = append(services, &ServiceCount{
			// A CHECK limits device_ports.port to 1-65535, so it fits.
			Port:    uint16(row.Port), //nolint:gosec // range enforced by the column CHECK.
			Service: row.Service.String, Devices: int(row.Devices),
		})
	}

	return &PortOverview{
		Open: int(stats.OpenPorts), Devices: int(stats.Devices),
		Opened: int(stats.Opened), Closed: int(stats.Closed), Services: services,
	}, nil
}

// RecordPorts merges a port scan into the inventory, under the named source.
// This is the same place as for the sweep, because the same host probes.
//
// A port scan is a third kind of reading, with a sweep and a source read. It
// asserts no presence, identifies no device, and has no name. So it does not
// go through the fact path. It shares the three phases of every reading: a
// scan row opens, the work goes in one transaction, and the row closes with
// the outcome. So an interrupted scan cannot leave a device half-updated.
func (s *Store) RecordPorts(
	ctx context.Context,
	source string,
	scans []scanner.PortScan,
) (*PortSummary, error) {
	return s.recordPortScan(ctx, source, dbtype.ScopeFull, scans)
}

// RecordNewDevicePorts merges a port scan of only the devices that a sweep just
// found into the inventory, as [Store.RecordPorts] does. Its scan is marked as
// a scan of new devices. So nothing takes it for the latest full port scan.
func (s *Store) RecordNewDevicePorts(
	ctx context.Context,
	source string,
	scans []scanner.PortScan,
) (*PortSummary, error) {
	return s.recordPortScan(ctx, source, dbtype.ScopeNewDevices, scans)
}

func (s *Store) recordPortScan(
	ctx context.Context,
	source string,
	scope dbtype.ScanScope,
	scans []scanner.PortScan,
) (*PortSummary, error) {
	// The port scan runs from the sweeping host. So it goes under the source
	// row of the sweep.
	scanID, _, err := s.openScan(ctx, source, dbtype.SourceSweep, dbtype.ScanPorts, scope, nil)
	if err != nil {
		return nil, err
	}

	sum, touched, ingestErr := s.ingestPorts(ctx, scanID, scans)

	found := 0
	if ingestErr == nil {
		found = sum.Open
	}

	closeErr := s.close(ctx, scanID, found, ingestErr)

	if err := errors.Join(ingestErr, closeErr); err != nil {
		return nil, fmt.Errorf("port scan %d: %w", scanID, err)
	}

	sum.ScanID = scanID

	// A port that opened or closed can change the guess of the classifier.
	// This is best-effort, as the pass after a discovery. An old icon is not a
	// reason to fail a scan that recorded the ports correctly.
	if err := s.reclassify(ctx, scanID, touched); err != nil {
		s.log.WarnContext(ctx, "classify pass after port scan failed", slog.Int64("scan", scanID), logger.Err(err))
	}

	s.scanFinished(ctx, scanID)

	return sum, nil
}

// ingestPorts applies every result as one transaction. So a scan goes in whole,
// or not at all. It returns the sorted ids of the devices for which it wrote a
// port, for the classify pass after the commit.
func (s *Store) ingestPorts(ctx context.Context, scanID int64, scans []scanner.PortScan) (*PortSummary, []int64, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin port ingest: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)
	at := s.stamp()
	sum := &PortSummary{Targets: len(scans)}

	found, err := s.gatherPorts(ctx, q, scans, sum)
	if err != nil {
		return nil, nil, err
	}

	sum.Devices = len(found)
	ids := slices.Sorted(maps.Keys(found))

	for _, id := range ids {
		if err := s.recordPorts(ctx, q, scanID, at, id, found[id], sum); err != nil {
			return nil, nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit port ingest: %w", err)
	}

	return sum, ids, nil
}

// devicePorts is what a scan found across every address of one device:
//   - the ports that answered on one of them
//   - the ports probed on one of them that answered the scan
//   - the ports probed only because the device asked for them, outside the
//     set of the scanner
type devicePorts struct {
	open    map[uint16]struct{}
	scanned map[uint16]struct{}
	extra   map[uint16]struct{}
}

// logged reports whether a change on port goes in the event log. It leaves out
// a port outside the set of the scanner. The scan probes it only because the
// device advertised a service there. A phone advertises on a new random port
// each time. A log entry for each would hide the changes that are worth a
// read.
func (d *devicePorts) logged(port uint16) bool {
	_, extra := d.extra[port]

	return !extra
}

// gatherPorts puts each result under the device that has its address now, with
// the device id as key. A device can have several addresses, such as a wired
// address and a Wi-Fi address. A service can listen on only one of them. So a
// port belongs to the device when one of its addresses answers on it. An
// address that answered nothing adds nothing to the scanned set. So a device
// that is asleep or gone keeps its ports.
func (s *Store) gatherPorts(
	ctx context.Context,
	q *models.Queries,
	scans []scanner.PortScan,
	sum *PortSummary,
) (map[int64]*devicePorts, error) {
	found := make(map[int64]*devicePorts)

	for _, scan := range scans {
		holder, err := currentHolder(ctx, q, dbtype.NewAddr(scan.Addr))
		if err != nil {
			return nil, fmt.Errorf("record ports for %s: %w", scan.Addr, err)
		}

		if holder == nil {
			s.log.DebugContext(ctx, "dropping a port result for an address no device holds", slog.String("addr", scan.Addr.String()))

			sum.Dropped++

			continue
		}

		d, ok := found[holder.ID]
		if !ok {
			d = &devicePorts{open: map[uint16]struct{}{}, scanned: map[uint16]struct{}{}, extra: map[uint16]struct{}{}}
			found[holder.ID] = d
		}

		for _, port := range scan.Open {
			d.open[port] = struct{}{}
		}

		for _, port := range scan.Extra {
			d.extra[port] = struct{}{}
		}

		if !scan.Answered {
			continue
		}

		for _, port := range scan.Scanned {
			d.scanned[port] = struct{}{}
		}
	}

	return found, nil
}

// recordPorts compares what a scan found across the addresses of a device with
// what the inventory has for it:
//   - a port that is newly open gets a row and an event
//   - a port that was open, was checked again, and answered on none of its
//     addresses changes to closed
//   - a port that the scan did not cover does not change, because this run
//     checked nothing about it
func (s *Store) recordPorts(
	ctx context.Context,
	q *models.Queries,
	scanID int64,
	at dbtype.Time,
	deviceID int64,
	found *devicePorts,
	sum *PortSummary,
) error {
	wasOpen, err := openPorts(ctx, q, deviceID)
	if err != nil {
		return err
	}

	sum.Open += len(found.open)

	for _, port := range slices.Sorted(maps.Keys(found.open)) {
		if err := s.openPort(ctx, q, scanID, at, deviceID, port, wasOpen, found.logged(port)); err != nil {
			return err
		}

		if _, already := wasOpen[port]; !already {
			sum.Opened++
		}
	}

	for _, port := range slices.Sorted(maps.Keys(wasOpen)) {
		_, stillOpen := found.open[port]
		_, looked := found.scanned[port]

		if stillOpen || !looked {
			continue
		}

		if err := s.closePort(ctx, q, scanID, at, deviceID, port, found.logged(port)); err != nil {
			return err
		}

		sum.Closed++
	}

	return nil
}

// openPort records a port as answering. If it was not already open and logged
// is set, it logs it.
func (s *Store) openPort(
	ctx context.Context,
	q *models.Queries,
	scanID int64,
	at dbtype.Time,
	deviceID int64,
	port uint16,
	wasOpen map[uint16]struct{},
	logged bool,
) error {
	err := q.UpsertOpenPort(ctx, models.UpsertOpenPortParams{
		DeviceID: deviceID,
		Port:     int64(port),
		Service:  nullString(scanner.ServiceName(port)),
		SeenAt:   at,
	})
	if err != nil {
		return fmt.Errorf("record port %d open on device %d: %w", port, deviceID, err)
	}

	if _, already := wasOpen[port]; already || !logged {
		return nil
	}

	return s.writeEvent(ctx, q, scanID, at, deviceID,
		dbtype.EventPortOpened, "", strconv.Itoa(int(port)), scanner.ServiceName(port))
}

// closePort changes a port that stopped answering to closed. When logged is
// set, it logs it.
func (s *Store) closePort(
	ctx context.Context,
	q *models.Queries,
	scanID int64,
	at dbtype.Time,
	deviceID int64,
	port uint16,
	logged bool,
) error {
	err := q.ClosePort(ctx, models.ClosePortParams{DeviceID: deviceID, Port: int64(port), SeenAt: at})
	if err != nil {
		return fmt.Errorf("record port %d closed on device %d: %w", port, deviceID, err)
	}

	if !logged {
		return nil
	}

	return s.writeEvent(ctx, q, scanID, at, deviceID,
		dbtype.EventPortClosed, strconv.Itoa(int(port)), "", scanner.ServiceName(port))
}

// openPorts reads the ports recorded as open on a device now into a set.
func openPorts(ctx context.Context, q *models.Queries, deviceID int64) (map[uint16]struct{}, error) {
	rows, err := q.ListDeviceOpenPorts(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("open ports of device %d: %w", deviceID, err)
	}

	set := make(map[uint16]struct{}, len(rows))
	for _, row := range rows {
		// A CHECK limits device_ports.port to 1-65535, so it fits.
		set[uint16(row.Port)] = struct{}{} //nolint:gosec // range enforced by the column CHECK.
	}

	return set, nil
}
