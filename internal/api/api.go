// Package api serves the inventory as JSON, for anything that reads it without
// a browser. It renders what internal/inventory returns rather than shaping the
// data itself, so it and internal/web cannot come to disagree about what a
// device is.
package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

type Handler struct {
	*http.ServeMux
	store      *inventory.Store
	jsonWriter *response.JSONWriter
	reader     *request.Reader
}

func NewHandler(l *slog.Logger, store *inventory.Store) http.Handler {
	jw := response.NewJSONWriter(
		l,
		response.WithErrorProblemMapper(problemFor),
	)

	h := &Handler{
		ServeMux:   http.NewServeMux(),
		store:      store,
		jsonWriter: jw,
		// The query types validate themselves, so the reader needs no
		// validator: the rules are which values a filter admits, which is
		// plainer as Go than as a struct tag.
		reader: request.NewReader(l, nil),
	}

	h.HandleFunc("GET /livez", jw.Handle(healthHandler(jw)))

	h.HandleFunc("GET /stats", jw.Handle(h.stats))
	h.HandleFunc("GET /groups", jw.Handle(h.groups))

	h.HandleFunc("GET /devices", jw.Handle(h.listDevices))
	h.HandleFunc("GET /devices/{id}", jw.Handle(h.getDevice))
	h.HandleFunc("GET /devices/{id}/events", jw.Handle(h.deviceEvents))

	h.HandleFunc("GET /events", jw.Handle(h.listEvents))
	h.HandleFunc("GET /scans", jw.Handle(h.listScans))

	return h
}

// problemFor renders the errors the inventory returns that are not simply
// failures. Anything else falls through to a generic 500, which is what an
// unexpected error deserves.
func problemFor(err error) response.Problem {
	if errors.Is(err, inventory.ErrNotFound) {
		return response.NewProblem().
			WithStatus(http.StatusNotFound).
			WithTitle(http.StatusText(http.StatusNotFound)).
			WithDetail(err.Error()).
			Build()
	}

	return nil
}

// badRequest reports a request the router matched but the handler cannot use,
// such as a path parameter that is not a number.
func badRequest(detail string) error {
	return &request.ReadError{
		HTTPStatusCode: http.StatusBadRequest,
		Message:        detail,
	}
}
