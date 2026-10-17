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

// The maximum length of each free-text curation field. The API, the web form
// and the MCP tool all limit an edit to these. The first two copy them in
// validate tags, because a tag cannot name a constant. Tests keep the two
// equal.
const (
	LabelMaxLength = 200
	GroupMaxLength = 100
	NotesMaxLength = 2000
)

// Curation is what the user owns on a device. No scan or plugin writes any of
// it. So it stays when the device moves to a new address or gets a new
// identity.
//
// The store applies every field as it is. A form sends all of them. So an
// empty field means cleared.
type Curation struct {
	Label   string
	Notes   string
	Group   string
	Type    string
	Ignored bool
}

// clean trims each field. Whitespace at the ends is never intended. Without the
// trim, a label of only spaces would be a name that renders as nothing.
//
// Type is one of the classes of the classifier, or nothing. It overrides the
// guess that sets the device icon. So clean drops a value that names no
// class. A stored value of that kind could never have an effect.
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

// CurationPatch is an edit to some of the curation of a device. A nil field
// keeps its value. A field set to "" clears it.
type CurationPatch struct {
	Label   *string
	Notes   *string
	Group   *string
	Type    *string
	Ignored *bool
}

// onto returns c, with the fields of p replaced.
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

// UpdateCuration applies c to a device, and records what it changed.
//
// The row and its events go in one transaction. For the log, a change that is
// not in the log did not happen. The log is the only record of how a device
// got its current state.
func (s *Store) UpdateCuration(ctx context.Context, id int64, c Curation) (*Device, error) {
	return s.curate(ctx, id, func(Curation) Curation { return c })
}

// PatchCuration applies the fields of p to a device, and keeps the rest. It
// records what it changed, as [Store.UpdateCuration] does.
//
// It reads the current curation in the same transaction as the write. So two
// patches to different fields cannot undo each other.
func (s *Store) PatchCuration(ctx context.Context, id int64, p CurationPatch) (*Device, error) {
	return s.curate(ctx, id, p.onto)
}

// curate writes the curation that change makes from the current curation of
// the device. It writes an event for each field that changed.
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

	// One event for each changed field. So the log tells which field, and the
	// new value. An edit that changed nothing writes nothing.
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

	// Read the device again. The updated row does not have the addresses of
	// the device. A caller that renders it from here would show a device with
	// no addresses.
	return s.Device(ctx, id)
}

// edit is one field that changed.
type edit struct {
	field string
	from  string
	to    string
}

// edits reports which fields of the user are different between two versions of
// a device. It compares nothing else. The user cannot change the columns of a
// scan. So a difference in one is not an edit.
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
