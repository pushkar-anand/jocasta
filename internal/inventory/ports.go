package inventory

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// PortSummary counts what recording one port scan changed.
type PortSummary struct {
	ScanID int64

	// Targets is how many addresses the scan reported on; Devices how many of
	// them resolved to a device the inventory still holds, and Dropped the rest
	// -- an address retired between the sweep that found it and this scan.
	Targets int
	Devices int
	Dropped int

	// Open is how many open ports were seen across those devices. Opened and
	// Closed count only the transitions: a port newly answering, and one that
	// was open and has gone quiet.
	Open   int
	Opened int
	Closed int
}

// RecordPorts folds a port scan into the inventory, attributing it to the named
// source -- the same vantage point the sweep records, since it is the same host
// probing.
//
// A port scan is a third kind of reading beside a sweep and a source read. It
// asserts no presence, identifies no device and carries no name, so it does not
// go through the fact path; but it shares the three phases every reading does --
// a scan row opened, the work in one transaction, the row closed with the
// outcome -- so an interrupted scan cannot leave a device half-updated.
func (s *Store) RecordPorts(
	ctx context.Context,
	source string,
	scans []scanner.PortScan,
) (*PortSummary, error) {
	// The port scan runs from the sweeping host, so it files under the sweep's
	// source row rather than one of its own.
	scanID, _, err := s.openScan(ctx, source, dbtype.SourceSweep, dbtype.ScanPorts, nil)
	if err != nil {
		return nil, err
	}

	sum, ingestErr := s.ingestPorts(ctx, scanID, scans)

	found := 0
	if ingestErr == nil {
		found = sum.Open
	}

	closeErr := s.close(ctx, scanID, found, ingestErr)

	if err := errors.Join(ingestErr, closeErr); err != nil {
		return nil, fmt.Errorf("port scan %d: %w", scanID, err)
	}

	sum.ScanID = scanID

	return sum, nil
}

// ingestPorts applies every result as one transaction, so a scan either lands
// whole or not at all.
func (s *Store) ingestPorts(ctx context.Context, scanID int64, scans []scanner.PortScan) (*PortSummary, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin port ingest: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)
	at := s.stamp()
	sum := &PortSummary{Targets: len(scans)}

	for _, scan := range scans {
		if err := s.recordPorts(ctx, q, scanID, at, scan, sum); err != nil {
			return nil, fmt.Errorf("record ports for %s: %w", scan.Addr, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit port ingest: %w", err)
	}

	return sum, nil
}

// recordPorts diffs one address's scan against what the inventory has for its
// device: a port newly open gets a row and an event, a port that was open and
// was looked at again but did not answer flips to closed, and a port the scan
// did not cover is left alone -- this run has no opinion on it.
func (s *Store) recordPorts(
	ctx context.Context,
	q *models.Queries,
	scanID int64,
	at dbtype.Time,
	scan scanner.PortScan,
	sum *PortSummary,
) error {
	ip := dbtype.NewAddr(scan.Addr)

	holder, err := currentHolder(ctx, q, ip)
	if err != nil {
		return err
	}

	if holder == nil {
		s.log.DebugContext(ctx, "dropping a port result for an address no device holds", "addr", scan.Addr)

		sum.Dropped++

		return nil
	}

	sum.Devices++
	sum.Open += len(scan.Open)

	wasOpen, err := openPorts(ctx, q, holder.ID)
	if err != nil {
		return err
	}

	openNow := make(map[uint16]struct{}, len(scan.Open))
	for _, port := range scan.Open {
		openNow[port] = struct{}{}
	}

	for _, port := range scan.Open {
		if err := s.openPort(ctx, q, scanID, at, holder.ID, port, wasOpen); err != nil {
			return err
		}

		if _, already := wasOpen[port]; !already {
			sum.Opened++
		}
	}

	scannedNow := make(map[uint16]struct{}, len(scan.Scanned))
	for _, port := range scan.Scanned {
		scannedNow[port] = struct{}{}
	}

	for port := range wasOpen {
		_, stillOpen := openNow[port]
		_, looked := scannedNow[port]

		if stillOpen || !looked {
			continue
		}

		if err := s.closePort(ctx, q, scanID, at, holder.ID, port); err != nil {
			return err
		}

		sum.Closed++
	}

	return nil
}

// openPort records a port as answering and, when it was not already, logs it.
func (s *Store) openPort(
	ctx context.Context,
	q *models.Queries,
	scanID int64,
	at dbtype.Time,
	deviceID int64,
	port uint16,
	wasOpen map[uint16]struct{},
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

	if _, already := wasOpen[port]; already {
		return nil
	}

	return s.writeEvent(ctx, q, scanID, at, deviceID,
		dbtype.EventPortOpened, "", strconv.Itoa(int(port)), scanner.ServiceName(port))
}

// closePort flips a port that has stopped answering and logs it.
func (s *Store) closePort(
	ctx context.Context,
	q *models.Queries,
	scanID int64,
	at dbtype.Time,
	deviceID int64,
	port uint16,
) error {
	err := q.ClosePort(ctx, models.ClosePortParams{DeviceID: deviceID, Port: int64(port), SeenAt: at})
	if err != nil {
		return fmt.Errorf("record port %d closed on device %d: %w", port, deviceID, err)
	}

	return s.writeEvent(ctx, q, scanID, at, deviceID,
		dbtype.EventPortClosed, strconv.Itoa(int(port)), "", scanner.ServiceName(port))
}

// openPorts reads the ports currently recorded open on a device into a set.
func openPorts(ctx context.Context, q *models.Queries, deviceID int64) (map[uint16]struct{}, error) {
	rows, err := q.ListDeviceOpenPorts(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("open ports of device %d: %w", deviceID, err)
	}

	set := make(map[uint16]struct{}, len(rows))
	for _, row := range rows {
		// device_ports.port is CHECK-constrained to 1-65535, so it fits.
		set[uint16(row.Port)] = struct{}{} //nolint:gosec // range enforced by the column CHECK.
	}

	return set, nil
}
