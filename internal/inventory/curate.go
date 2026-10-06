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

// The longest each free-text curation field may be. The API, the web form and
// the MCP tool all hold an edit to these; the first two spell them out in
// validate tags, since a tag cannot name a constant, and tests keep the two in
// step.
const (
	LabelMaxLength = 200
	GroupMaxLength = 100
	NotesMaxLength = 2000
)

// Curation is what the user owns on a device. No scan or plugin writes any of
// it, which is why it survives the device moving address or being re-identified.
//
// Every field is applied as given: a form submits all of them, so a field left
// empty means cleared.
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
// that drives the device icon, so a value that names no class is dropped.
// Stored, it could never take effect.
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

// CurationPatch is an edit to some of a device's curation. A nil field keeps
// its value; a field set to "" clears it.
type CurationPatch struct {
	Label   *string
	Notes   *string
	Group   *string
	Type    *string
	Ignored *bool
}

// onto returns c with the fields p carries replaced.
func (p CurationPatch) onto(c Curation) Curation {
	if p.Label != nil {
		c.Label = *p.Label
	}

	if p.Notes != nil {
		c.Notes = *p.Notes
	}

	if p.Group != nil {
		c.Group = *p.Group
	}

	if p.Type != nil {
		c.Type = *p.Type
	}

	if p.Ignored != nil {
		c.Ignored = *p.Ignored
	}

	return c
}

// UpdateCuration applies c to a device and records what it changed.
//
// The row and its events are written in one transaction: a change that is not
// in the log did not happen as far as the log is concerned, and the log is the
// only account of how a device came to look the way it does.
func (s *Store) UpdateCuration(ctx context.Context, id int64, c Curation) (*Device, error) {
	return s.curate(ctx, id, func(Curation) Curation { return c })
}

// PatchCuration applies the fields p carries to a device, keeps the rest, and
// records what it changed, as [Store.UpdateCuration] does.
//
// The current curation is read in the same transaction as the write, so two
// patches to different fields cannot undo each other.
func (s *Store) PatchCuration(ctx context.Context, id int64, p CurationPatch) (*Device, error) {
	return s.curate(ctx, id, p.onto)
}

// curate writes the curation that change derives from the device's current one,
// and an event for each field that moved.
func (s *Store) curate(ctx context.Context, id int64, change func(Curation) Curation) (*Device, error) {
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

	c := change(Curation{
		Label:   before.Label.String,
		Notes:   before.Notes.String,
		Group:   before.GroupName.String,
		Type:    before.DeviceType.String,
		Ignored: before.IsIgnored,
	}).clean()

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

	// Re-read the device: the updated row lacks the addresses a device
	// carries, and a caller re-rendering one from here would otherwise show
	// a device that holds none.
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
