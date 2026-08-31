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
	*http.ServeMux
	reader     *request.Reader
	jsonWriter *response.JSONWriter
}

func NewHandler(
	l *slog.Logger,
	reader *request.Reader,
	store *inventory.Store,
) http.Handler {
	jw := response.NewJSONWriter(
		l,
		response.WithErrorProblemMapper(problemFor),
	)

	h := &Handler{
		ServeMux:   http.NewServeMux(),
		reader:     reader,
		jsonWriter: jw,
	}

	h.HandleFunc("GET /livez", jw.Handle(h.healthHandler()))

	h.HandleFunc("GET /stats", jw.Handle(h.stats(store)))
	h.HandleFunc("GET /groups", jw.Handle(h.groups(store)))

	h.HandleFunc("GET /devices", jw.Handle(h.listDevices(store)))
	h.HandleFunc("GET /devices/{id}", jw.Handle(h.getDevice(store)))
	h.HandleFunc("GET /devices/{id}/events", jw.Handle(h.deviceEvents(store)))

	h.HandleFunc("GET /events", jw.Handle(h.listEvents(store)))
	h.HandleFunc("GET /scans", jw.Handle(h.listScans(store)))

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
