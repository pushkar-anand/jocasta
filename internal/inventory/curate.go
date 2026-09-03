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

// Curation is what the user owns on a device. No scan or plugin writes any of
// it, which is why it survives the device moving address or being re-identified.
//
// Every field is applied, not merged: a form submits all of them, so a field
// left empty means cleared rather than unchanged.
type Curation struct {
	Label   string
	Notes   string
	Group   string
	Type    string
	Ignored bool
}

// clean trims each field. Surrounding whitespace is never meant, and a label of
// only spaces would otherwise be a name that renders as nothing.
//
// Type is one of the classifier's classes or nothing: it overrides the guess
// that drives the device icon, so a value that names no class is dropped rather
// than stored where it could never take effect.
func (c Curation) clean() Curation {
	kind := strings.TrimSpace(c.Type)
	if !classify.Class(kind).Valid() {
		kind = ""
	}

	return Curation{
		Label:   strings.TrimSpace(c.Label),
		Notes:   strings.TrimSpace(c.Notes),
		Group:   strings.TrimSpace(c.Group),
		Type:    kind,
		Ignored: c.Ignored,
	}
}

// UpdateCuration applies c to a device and records what it changed.
//
// The row and its events are written in one transaction: a change that is not
// in the log did not happen as far as the log is concerned, and the log is the
// only account of how a device came to look the way it does.
func (s *Store) UpdateCuration(ctx context.Context, id int64, c Curation) (*Device, error) {
	c = c.clean()

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin curation: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)

	before, err := q.GetDevice(ctx, id)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("device %d: %w", id, ErrNotFound)
	case err != nil:
		return nil, fmt.Errorf("device %d: %w", id, err)
	}

	after, err := q.UpdateDeviceCuration(ctx, models.UpdateDeviceCurationParams{
		Label:      nullString(c.Label),
		Notes:      nullString(c.Notes),
		GroupName:  nullString(c.Group),
		DeviceType: nullString(c.Type),
		IsIgnored:  c.Ignored,
		ID:         id,
	})
	if err != nil {
		return nil, fmt.Errorf("curate device %d: %w", id, err)
	}

	// One event per field that moved, so the log says which one and to what.
	// An edit that changed nothing writes nothing.
	at := s.stamp()

	for _, e := range edits(before, after) {
		params := models.CreateEventParams{
			DeviceID:   sql.NullInt64{Int64: id, Valid: true},
			Kind:       dbtype.EventDeviceEdited,
			OldValue:   nullString(e.from),
			NewValue:   nullString(e.to),
			Detail:     nullString(e.field),
			OccurredAt: at,
		}

		if err := q.CreateEvent(ctx, params); err != nil {
			return nil, fmt.Errorf("record edit of device %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit curation: %w", err)
	}

	// Re-read rather than convert the updated row: a device carries the
	// addresses it holds, and a caller re-rendering one from here would
	// otherwise show a device that holds none.
	return s.Device(ctx, id)
}

// edit is one field that changed.
type edit struct {
	field string
	from  string
	to    string
}

// edits reports which user-owned fields differ between two versions of a
// device. Nothing else is compared: a scan's columns are not the user's to
// change, so a difference in one is not an edit.
func edits(before, after *models.Device) []edit {
	candidates := []edit{
		{field: "label", from: before.Label.String, to: after.Label.String},
		{field: "notes", from: before.Notes.String, to: after.Notes.String},
		{field: "group", from: before.GroupName.String, to: after.GroupName.String},
		{field: "type", from: before.DeviceType.String, to: after.DeviceType.String},
		{field: "ignored", from: yesNo(before.IsIgnored), to: yesNo(after.IsIgnored)},
	}

	changed := make([]edit, 0, len(candidates))

	for _, c := range candidates {
		if c.from != c.to {
			changed = append(changed, c)
		}
	}

	return changed
}

// yesNo renders a flag for the log, where every value is text.
func yesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}
