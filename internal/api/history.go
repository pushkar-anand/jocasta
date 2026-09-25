package api

import (
	"cmp"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

func (h *Handler) listEvents(store *inventory.Store) response.HandlerFunc {
	type (
		// Cursor decodes itself out of the query string, so a client hands back
		// the token the last page gave it and nothing here takes it apart. An
		// unset limit gets the default, which is what omitempty buys; a limit
		// that was asked for has to be usable. max is inventory.MaxPageSize,
		// written out because a tag cannot name a constant.
		eventsRequest struct {
			Limit  int              `schema:"limit" validate:"omitempty,min=1,max=500"`
			Cursor inventory.Cursor `schema:"cursor"`
		}

		eventsResponse struct {
			Events []*inventory.Event `json:"events"`
			Count  int                `json:"count"`

			// NextCursor is what to ask for to get the window after this one,
			// and is absent once the log has been read to the end.
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
