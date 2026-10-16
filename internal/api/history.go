package api

import (
	"cmp"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// listEvents serves one page of changes, newest first. If more changes remain,
// it adds a cursor for the next page.
func (h *Handler) listEvents(store *inventory.Store) response.HandlerFunc {
	type (
		// Cursor decodes itself from the query string. So a client sends back
		// the token from the last page, and this code does not take it apart.
		// An unset limit gets the default, because of omitempty. A limit that
		// the client sets must be usable. max is inventory.MaxPageSize, as a
		// number, because a tag cannot name a constant.
		eventsRequest struct {
			Limit  int              `schema:"limit" validate:"omitempty,min=1,max=500"`
			Cursor inventory.Cursor `schema:"cursor"`
		}

		eventsResponse struct {
			Events []*inventory.Event `json:"events"`
			Count  int                `json:"count"`

			// NextCursor is the cursor for the next window. It is absent when
			// the client has read the log to the end.
			NextCursor inventory.Cursor `json:"next_cursor,omitzero"`
		}
	)

	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[eventsRequest](r)
		if err != nil {
			return err
		}

		page, err := store.ListEvents(r.Context(), inventory.Page{
			Limit:  cmp.Or(q.Limit, inventory.DefaultPageSize),
			Cursor: q.Cursor,
		})
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, eventsResponse{
			Events:     page.Events,
			Count:      len(page.Events),
			NextCursor: page.Next,
		})

		return nil
	}
}

// listScans serves one page of recorded scans, newest first. If more scans
// remain, it adds a cursor for the next page. It does not start a scan.
func (h *Handler) listScans(store *inventory.Store) response.HandlerFunc {
	type (
		scansRequest struct {
			Limit  int              `schema:"limit" validate:"omitempty,min=1,max=500"`
			Cursor inventory.Cursor `schema:"cursor"`
		}

		scansResponse struct {
			Scans      []*inventory.Scan `json:"scans"`
			Count      int               `json:"count"`
			NextCursor inventory.Cursor  `json:"next_cursor,omitzero"`
		}
	)

	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[scansRequest](r)
		if err != nil {
			return err
		}

		page, err := store.ListScans(r.Context(), inventory.Page{
			Limit:  cmp.Or(q.Limit, inventory.DefaultPageSize),
			Cursor: q.Cursor,
		})
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, scansResponse{
			Scans:      page.Scans,
			Count:      len(page.Scans),
			NextCursor: page.Next,
		})

		return nil
	}
}
