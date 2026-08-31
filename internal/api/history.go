package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// Events and scans are the two logs that grow without bound, so both are paged.
const (
	defaultPageSize = 50
	maxPageSize     = 500
)

type scansResponse struct {
	Scans []inventory.Scan `json:"scans"`
	Count int              `json:"count"`
}

// pageQuery is one window onto a log.
type pageQuery struct {
	Limit  int `schema:"limit"`
	Offset int `schema:"offset"`
}

// Valid implements request.SelfValidator. An unset limit is the default rather
// than a rejected zero, but a limit that was asked for has to be usable.
func (q pageQuery) Valid(context.Context) map[string]string {
	problems := make(map[string]string)

	if q.Limit < 0 || q.Limit > maxPageSize {
		problems["limit"] = fmt.Sprintf("must be between 1 and %d", maxPageSize)
	}

	if q.Offset < 0 {
		problems["offset"] = "must not be negative"
	}

	return problems
}

func (q pageQuery) window() (limit, offset int) {
	if q.Limit == 0 {
		return defaultPageSize, q.Offset
	}

	return q.Limit, q.Offset
}

func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) error {
	q, err := h.reader.ReadAndValidateQueryParams[pageQuery](r)
	if err != nil {
		return err
	}

	limit, offset := q.window()

	events, err := h.store.ListEvents(r.Context(), limit, offset)
	if err != nil {
		return err
	}

	h.jsonWriter.Ok(w, r, eventsResponse{Events: events, Count: len(events)})

	return nil
}

func (h *Handler) listScans(w http.ResponseWriter, r *http.Request) error {
	q, err := h.reader.ReadAndValidateQueryParams[pageQuery](r)
	if err != nil {
		return err
	}

	limit, offset := q.window()

	scans, err := h.store.ListScans(r.Context(), limit, offset)
	if err != nil {
		return err
	}

	h.jsonWriter.Ok(w, r, scansResponse{Scans: scans, Count: len(scans)})

	return nil
}
