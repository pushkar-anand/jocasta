package web

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// logPageSize is how many entries a page of either log shows.
const logPageSize = 50

// logData is one page of a log that grows with no limit.
type logData struct {
	view
	Events []*inventory.Event
	Scans  []*inventory.Scan

	// Path is the address of the log, before a filter: /events or /scans.
	Path string

	// Device is the one device of the change log filter. It is nil for the
	// full log. Only the event log has this filter.
	Device *inventory.Device

	// Kind limits the scan log to one kind ("discovery", "ports"). It is empty
	// for every kind. Only the scan log has this filter.
	Kind string

	// Cursor is the token of this page. It is empty at the top of the log.
	Cursor string

	// Next is the token for the next page. It is empty when the reader
	// reached the end of the log.
	Next string
}

// AtTop reports whether this is the first page.
func (d logData) AtTop() bool { return d.Cursor == "" }

// HasOlder reports whether there is a next page.
func (d logData) HasOlder() bool { return d.Next != "" }

// Top is the address of the first page of this log, with its filter. So a walk
// back to the top does not make the log wider.
func (d logData) Top() string { return withQuery(d.Path, d.narrowing()) }

// Older is the address of the next page, with the filter.
func (d logData) Older() string {
	q := d.narrowing()
	q.Set("cursor", d.Next)

	return withQuery(d.Path, q)
}

// narrowing is the filter of this log, as query parameters.
func (d logData) narrowing() url.Values {
	q := url.Values{}

	if d.Device != nil {
		q.Set("device", strconv.FormatInt(d.Device.ID, 10))
	}

	if d.Kind != "" {
		q.Set("kind", d.Kind)
	}

	return q
}

// logQuery is the cursor from which a log page continues.
type logQuery struct {
	Cursor string `schema:"cursor" validate:"omitempty,max=512"`
}

// scansQuery is the query string of the scan log: its cursor, and its kind
// filter, if there is one.
type scansQuery struct {
	logQuery
	Kind string `schema:"kind" validate:"omitempty,oneof=discovery ports"`
}

// scanKind maps the query-string spelling to the stored value. An empty or
// unknown value means every kind.
func scanKind(s string) dbtype.ScanKind {
	switch s {
	case "discovery":
		return dbtype.ScanDiscovery
	case "ports":
		return dbtype.ScanPorts
	}

	return ""
}

// eventsQuery is the query string of the events log: its cursor, and its device
// filter, if there is one.
//
// Device stays a string. A value that names no device is a missing page. So
// the handler turns it into inventory.ErrNotFound itself.
type eventsQuery struct {
	logQuery
	Device string `schema:"device" validate:"omitempty,max=20"`
}

// logCursor decodes the position in the log from a validated query. It reports
// the token that really applies.
//
// A cursor walks only forward. So a value that is not a position starts at the
// top. This is best for a reader who edited the address by hand. Also, a page
// has no place to report a bad token in a useful way.
func logCursor(raw string) (inventory.Cursor, string) {
	if raw == "" {
		return inventory.Cursor{}, ""
	}

	var c inventory.Cursor
	if err := c.Decode(raw); err != nil {
		return inventory.Cursor{}, ""
	}

	return c, raw
}

// events serves a page of changes, for one existing device if asked. An
// unreadable cursor gives the newest page.
func (h *Handler) events(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		q, err := h.reader.ReadAndValidateQueryParams[eventsQuery](r)
		if err != nil {
			return err
		}

		from, token := logCursor(q.Cursor)

		data := &logData{
			Title: "Events", Section: "Events", Role: sm.CurrentRole(ctx),
			SignedInAs: sm.CurrentUsername(ctx),
			Path:       "/events",
			Cursor:     token,
		}

		window := inventory.Page{Limit: logPageSize, Cursor: from}

		// ?device= limits the log to one device. So the history of the device
		// page reaches the rest of the history. An id that names no device is
		// a missing page, as for /devices/{id}.
		if q.Device != "" {
			id, err := strconv.ParseInt(q.Device, 10, 64)
			if err != nil || id < 1 {
				return inventory.ErrNotFound
			}

			device, err := h.store.Device(ctx, id)
			if err != nil {
				return err
			}

			data.Device = device
			data.Crumb = &crumb{Label: device.Name(), Href: "/devices/" + strconv.FormatInt(id, 10)}
			window.Device = device.ID
		}

		page, err := h.store.ListEvents(ctx, window)
		if err != nil {
			h.log.ErrorContext(ctx, "failed to list events", logger.Err(err))

			return err
		}

		data.Events = page.Events
		data.Next = nextToken(page.Next)

		data.Note = latestSweepNote(ctx, h.store)

		h.htmlWriter.Success(w, r, templatePageEvents, data)

		return nil
	}
}

// scans serves a page of recorded scans, filtered by kind if asked. An
// unreadable cursor gives the newest page.
func (h *Handler) scans(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		q, err := h.reader.ReadAndValidateQueryParams[scansQuery](r)
		if err != nil {
			return err
		}

		from, token := logCursor(q.Cursor)

		data := &logData{
			Title: "Scans", Section: "Scans", Role: sm.CurrentRole(ctx),
			SignedInAs: sm.CurrentUsername(ctx),
			Path:       "/scans",
			Cursor:     token,
			Kind:       q.Kind,
		}

		page, err := h.store.ListScans(ctx, inventory.Page{
			Limit: logPageSize, Cursor: from, ScanKind: scanKind(q.Kind),
		})
		if err != nil {
			h.log.ErrorContext(ctx, "failed to list scans", logger.Err(err))

			return err
		}

		data.Scans = page.Scans
		data.Next = nextToken(page.Next)

		data.Note = latestSweepNote(ctx, h.store)

		h.htmlWriter.Success(w, r, templatePageScans, data)

		return nil
	}
}

// nextToken renders the cursor on which a page ended. A cursor that does not
// encode counts as the end of the log. The current page is still right. The
// alternative is a link that does not work.
func nextToken(c inventory.Cursor) string {
	token, err := c.Encode()
	if err != nil {
		return ""
	}

	return token
}
