package api

import (
	"cmp"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// defaultPageSize is the page size a request gets when it names none. Events
// and scans are the two logs that grow without bound, so both are paged, and
// the max rule on each request's Limit sets the ceiling.
const defaultPageSize = 50

func (h *Handler) listEvents(store *inventory.Store) response.HandlerFunc {
	type (
		// Cursor decodes itself out of the query string, so a client hands back
		// the token the last page gave it and nothing here takes it apart. An
		// unset limit gets the default, which is what omitempty buys; a limit
		// that was asked for has to be usable.
		eventsRequest struct {
			Limit  int              `schema:"limit" validate:"omitempty,min=1,max=500"`
			Cursor inventory.Cursor `schema:"cursor"`
		}

		eventsResponse struct {
			Events []*inventory.Event `json:"events"`
			Count  int                `json:"count"`

			// NextCursor is what to ask for to get the window after this one,
			// and is absent once the log has been read to the end.
			NextCursor *inventory.Cursor `json:"next_cursor,omitempty"`
		}
	)

	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[eventsRequest](r)
		if err != nil {
			return err
		}

		page, err := store.ListEvents(r.Context(), inventory.Page{
			Limit:  cmp.Or(q.Limit, defaultPageSize),
			Cursor: q.Cursor,
		})
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, eventsResponse{
			Events:     page.Events,
			Count:      len(page.Events),
			NextCursor: nextCursor(page.Next),
		})

		return nil
	}
}

func (h *Handler) listScans(store *inventory.Store) response.HandlerFunc {
	type (
		scansRequest struct {
			Limit  int              `schema:"limit" validate:"omitempty,min=1,max=500"`
			Cursor inventory.Cursor `schema:"cursor"`
		}

		scansResponse struct {
			Scans      []*inventory.Scan `json:"scans"`
			Count      int               `json:"count"`
			NextCursor *inventory.Cursor `json:"next_cursor,omitempty"`
		}
	)

	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[scansRequest](r)
		if err != nil {
			return err
		}

		page, err := store.ListScans(r.Context(), inventory.Page{
			Limit:  cmp.Or(q.Limit, defaultPageSize),
			Cursor: q.Cursor,
		})
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, scansResponse{
			Scans:      page.Scans,
			Count:      len(page.Scans),
			NextCursor: nextCursor(page.Next),
		})

		return nil
	}
}

// nextCursor renders the cursor a page ended on, or nil when it ended the
// log. The member is then omitted, and its absence tells a client to stop
// without testing for a null.
func nextCursor(c inventory.Cursor) *inventory.Cursor {
	if c.IsZero() {
		return nil
	}

	return &c
}
