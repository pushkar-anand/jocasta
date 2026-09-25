package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// OnScanFinished has the store call fn with the id of every scan that
// finishes successfully, once its changes and the classify pass after it are
// committed. fn runs on the scanning goroutine, so it must return at once.
//
// Call it before any scan is recorded: the store reads fn without a lock.
func (s *Store) OnScanFinished(fn func(ctx context.Context, scanID int64)) {
	s.onScan = fn
}

// ScanChanges is what one finished scan changed.
type ScanChanges struct {
	Kind    dbtype.ScanKind
	Source  string
	Network string // empty for a scan that covered no single network
	Found   int

	// First reports whether this is the first scan of its source, kind and
	// network to succeed, when every device in it is new to the inventory.
	First bool

	// Events are the changes, oldest first. Events about a device the owner
	// ignores are left out.
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
			e.DeviceName = spokenName(r.Label.String, r.Hostname.String, r.Vendor.String, macString(r.MAC), e.DeviceID)
		}

		c.Events = append(c.Events, e)
	}

	return c, nil
}

// spokenName names a device in a message read away from the inventory, where
// a hardware address means little: the owner's label, the hostname, then the
// vendor ("Espressif device"), and only then the address or id.
func spokenName(label, hostname, vendor, mac string, id int64) string {
	if label == "" && hostname == "" && vendor != "" {
		return vendor + " device"
	}

	return displayName(label, hostname, mac, "", id)
}
