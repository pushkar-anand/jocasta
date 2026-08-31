package web

import (
	"net/http"
	"net/url"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// logPageSize is how many entries a page of either log shows.
const logPageSize = 50

// logData is one page of a log that grows without bound.
type logData struct {
	view
	Events []inventory.Event
	Scans  []inventory.Scan

	// Path is the log's own address, which the pager links back to.
	Path string

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

// Older is the address of the page behind this one.
func (d logData) Older() string {
	return d.Path + "?cursor=" + url.QueryEscape(d.Next)
}

// logCursor reads the position in the log, and reports the token that is
// actually in effect.
//
// A cursor walks forwards only, so anything that is not a position starts at
// the top: that is where a reader who edited the address by hand is best
// served, and a page has nowhere to report a bad token that a reader could act
// on.
func logCursor(q url.Values) (inventory.Cursor, string) {
	raw := q.Get("cursor")
	if raw == "" {
		return inventory.Cursor{}, ""
	}

	var c inventory.Cursor
	if err := c.Decode(raw); err != nil {
		return inventory.Cursor{}, ""
	}

	return c, raw
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	from, token := logCursor(r.URL.Query())

	data := logData{
		view:   view{Title: "Activity", Section: "Activity"},
		Path:   "/events",
		Cursor: token,
	}

	page, err := h.store.ListEvents(ctx, inventory.Page{Limit: logPageSize, Cursor: from})
	if err != nil {
		h.fail(w, r, err)

		return
	}

	data.Events = page.Events
	data.Next = nextToken(page.Next)

	if note, err := h.sweepNote(ctx); err == nil {
		data.Note = note
	}

	h.renderer.Render(w, r, "page/events", data)
}

func (h *Handler) scans(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	from, token := logCursor(r.URL.Query())

	data := logData{
		view:   view{Title: "Sweeps", Section: "Sweeps"},
		Path:   "/scans",
		Cursor: token,
	}

	page, err := h.store.ListScans(ctx, inventory.Page{Limit: logPageSize, Cursor: from})
	if err != nil {
		h.fail(w, r, err)

		return
	}

	data.Scans = page.Scans
	data.Next = nextToken(page.Next)

	if note, err := h.sweepNote(ctx); err == nil {
		data.Note = note
	}

	h.renderer.Render(w, r, "page/scans", data)
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
