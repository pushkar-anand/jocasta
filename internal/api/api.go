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

// Handler holds what every handler needs to read a request and write a
// response. What a handler reads *about* -- the store -- is passed to it when
// it is built, so a route's dependencies are visible where it is registered
// rather than reachable from any handler that happens to have a receiver.
type Handler struct {
	mux        *http.ServeMux
	reader     *request.Reader
	jsonWriter *response.JSONWriter
}

// ServeHTTP routes a request to the JSON handler that matches it.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// NewHandler builds the JSON API routes over the given store.
func NewHandler(
	l *slog.Logger,
	reader *request.Reader,
	store *inventory.Store,
) *Handler {
	jw := response.NewJSONWriter(
		l,
		response.WithErrorProblemMapper(problemFor),
	)

	h := &Handler{
		mux:        http.NewServeMux(),
		reader:     reader,
		jsonWriter: jw,
	}

	h.mux.HandleFunc("GET /livez", jw.Handle(h.healthHandler()))

	h.mux.HandleFunc("GET /stats", jw.Handle(h.stats(store)))
	h.mux.HandleFunc("GET /groups", jw.Handle(h.groups(store)))

	h.mux.HandleFunc("GET /devices", jw.Handle(h.listDevices(store)))
	h.mux.HandleFunc("GET /devices/{id}", jw.Handle(h.getDevice(store)))
	h.mux.HandleFunc("PATCH /devices/{id}", jw.Handle(h.updateDevice(store)))
	h.mux.HandleFunc("GET /devices/{id}/events", jw.Handle(h.deviceEvents(store)))

	h.mux.HandleFunc("GET /events", jw.Handle(h.listEvents(store)))
	h.mux.HandleFunc("GET /scans", jw.Handle(h.listScans(store)))

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
