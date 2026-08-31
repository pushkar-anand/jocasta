package api

import (
	"cmp"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// Events and scans are the two logs that grow without bound, so both are paged.
// defaultPageSize is the window a request that asks for no particular one gets;
// the ceiling is the max rule on each request's Limit.
const defaultPageSize = 50

func (h *Handler) listEvents(store *inventory.Store) response.HandlerFunc {
	type (
		// An unset limit is the default rather than a rejected zero, which is
		// what omitempty buys; a limit that was asked for has to be usable.
		eventsRequest struct {
			Limit  int `schema:"limit" validate:"omitempty,min=1,max=500"`
			Offset int `schema:"offset" validate:"min=0"`
		}

		eventsResponse struct {
			Events []inventory.Event `json:"events"`
			Count  int               `json:"count"`
		}
	)

	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[eventsRequest](r)
		if err != nil {
			return err
		}

		events, err := store.ListEvents(r.Context(), cmp.Or(q.Limit, defaultPageSize), q.Offset)
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, eventsResponse{Events: events, Count: len(events)})

		return nil
	}
}

func (h *Handler) listScans(store *inventory.Store) response.HandlerFunc {
	type (
		scansRequest struct {
			Limit  int `schema:"limit" validate:"omitempty,min=1,max=500"`
			Offset int `schema:"offset" validate:"min=0"`
		}

		scansResponse struct {
			Scans []inventory.Scan `json:"scans"`
			Count int              `json:"count"`
		}
	)

	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[scansRequest](r)
		if err != nil {
			return err
		}

		scans, err := store.ListScans(r.Context(), cmp.Or(q.Limit, defaultPageSize), q.Offset)
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, scansResponse{Scans: scans, Count: len(scans)})

		return nil
	}
}
