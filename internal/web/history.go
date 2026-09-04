package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// logPageSize is how many entries a page of either log shows.
const logPageSize = 50

// logData is one page of a log that grows without bound.
type logData struct {
	view
	Events []*inventory.Event
	Scans  []*inventory.Scan

	// Path is the log's own address, before any narrowing: /events or /scans.
	Path string

	// Device is the one device the change log is narrowed to, and nil for the
	// full log. Only the event log is ever narrowed.
	Device *inventory.Device

	// Cursor is the token this page was reached by, and is empty at the top of
	// the log.
	Cursor string

	// Next is the token for the page behind this one, and is empty once the
	// log has been read to the end.
	Next string
}

// AtTop reports whether this is the first page.
func (d logData) AtTop() bool { return d.Cursor == "" }

// HasOlder reports whether there is another page behind this one.
func (d logData) HasOlder() bool { return d.Next != "" }

// Top is the address of the first page of this log, carrying the device filter
// where there is one so walking back to the top does not widen the log.
func (d logData) Top() string {
	if d.Device == nil {
		return d.Path
	}

	return d.Path + "?device=" + strconv.FormatInt(d.Device.ID, 10)
}

// Older is the address of the page behind this one, keeping the filter.
func (d logData) Older() string {
	sep := "?"
	if strings.Contains(d.Top(), "?") {
		sep = "&"
	}

	return d.Top() + sep + "cursor=" + url.QueryEscape(d.Next)
}

// logQuery is the cursor a log page continues from.
type logQuery struct {
	Cursor string `schema:"cursor" validate:"omitempty,max=512"`
}

// eventsQuery is the events log's own query string: its cursor, plus the
// device it narrows to when there is one.
//
// Device is kept as a string and not parsed here: a value that names no
// device is a page that is not there, not a bad request, so the handler turns
// it into inventory.ErrNotFound itself rather than a validation error.
type eventsQuery struct {
	logQuery
	Device string `schema:"device" validate:"omitempty,max=20"`
}

// logCursor decodes the position in the log out of an already-validated query,
// and reports the token that is actually in effect.
//
// A cursor walks forwards only, so anything that is not a position starts at
// the top: that is where a reader who edited the address by hand is best
// served, and a page has nowhere to report a bad token that a reader could act
// on.
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

func (h *Handler) events() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		q, err := h.reader.ReadAndValidateQueryParams[eventsQuery](r)
		if err != nil {
			return err
		}

		from, token := logCursor(q.Cursor)

		data := &logData{
			Title: "Events", Section: "Events",
			Path:   "/events",
			Cursor: token,
		}

		window := inventory.Page{Limit: logPageSize, Cursor: from}

		// ?device= narrows the log to one device: the way the device page's
		// history reaches the rest of itself. An id that names no device is a
		// page that is not there, the same as /devices/{id}.
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

		if note, err := h.sweepNote(ctx); err == nil {
			data.Note = note
		}

		h.htmlWriter.Success(w, r, templatePageEvents, data)
		return nil
	}
}

func (h *Handler) scans() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		q, err := h.reader.ReadAndValidateQueryParams[logQuery](r)
		if err != nil {
			return err
		}

		from, token := logCursor(q.Cursor)

		data := &logData{
			Title: "Scans", Section: "Scans",
			Path:   "/scans",
			Cursor: token,
		}

		page, err := h.store.ListScans(ctx, inventory.Page{Limit: logPageSize, Cursor: from})
		if err != nil {
			h.log.ErrorContext(ctx, "failed to list scans", logger.Err(err))

			return err
		}

		data.Scans = page.Scans
		data.Next = nextToken(page.Next)

		if note, err := h.sweepNote(ctx); err == nil {
			data.Note = note
		}

		h.htmlWriter.Success(w, r, templatePageScans, data)
		return nil
	}
}

// nextToken renders the cursor a page ended on. A cursor that will not encode
// is treated as the end of the log: the page a reader is looking at is still
// right, and the alternative is a link that cannot be followed.
func nextToken(c inventory.Cursor) string {
	token, err := c.Encode()
	if err != nil {
		return ""
	}

	return token
}
