package inventory

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// OnScanFinished makes the store call fn with the id of every successful scan.
// The call comes after its changes and the next classify pass committed. fn
// runs on the scanning goroutine, so it must return at once.
//
// Call it before the store records a scan. The store reads fn without a
// lock.
func (s *Store) OnScanFinished(fn func(ctx context.Context, scanID int64)) {
	s.onScan = fn
}

// ScanChanges is what one finished scan changed.
type ScanChanges struct {
	Kind    dbtype.ScanKind
	Source  string
	Network string // empty for a scan that covered no single network
	Found   int

	// First reports whether this is the first successful scan of its source,
	// kind and network. It can find devices that a different source already
	// recorded.
	First bool

	// Events are the changes, oldest first. It leaves out events about a
	// device that the owner ignores.
	Events []*Event
}

// ScanChanges returns what the scan with the given id changed. It returns
// ErrNotFound when no scan has that id.
func (s *Store) ScanChanges(ctx context.Context, scanID int64) (*ScanChanges, error) {
	sum, err := s.q.ScanSummary(ctx, scanID)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("scan %d: %w", scanID, ErrNotFound)
	case err != nil:
		return nil, fmt.Errorf("scan %d: %w", scanID, err)
	}

	rows, err := s.q.ScanEvents(ctx, sql.NullInt64{Int64: scanID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("changes of scan %d: %w", scanID, err)
	}

	c := &ScanChanges{
		Kind:    sum.Kind,
		Source:  sum.Source,
		Network: sum.Network,
		Found:   int(sum.FoundCount),
		First:   sum.IsFirst != 0,
		Events:  make([]*Event, 0, len(rows)),
	}

	for _, r := range rows {
		e := newEvent(&r.Event)
		if e.DeviceID != 0 {
			e.DeviceName = spokenName(r.Label.String, r.Hostname.String, cmp.Or(r.Vendor.String, r.VendorShort.String), macString(r.MAC), e.DeviceID)
		}

		c.Events = append(c.Events, e)
	}

	return c, nil
}

// spokenName names a device in a message that a person reads outside the
// inventory. There, a hardware address means little. The order is the label of
// the owner, the hostname, then the vendor ("Espressif device"), and only then
// the address or id.
func spokenName(label, hostname, vendor, mac string, id int64) string {
	if label == "" && hostname == "" && vendor != "" {
		return vendor + " device"
	}

	return displayName(label, hostname, mac, "", id)
}
