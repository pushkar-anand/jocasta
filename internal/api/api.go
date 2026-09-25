// Package api serves the inventory as JSON, for anything that reads it without
// a browser. It renders what internal/inventory returns and leaves shaping the
// data to that package, so it and internal/web cannot come to disagree about
// what a device is.
package api

import (
	"log/slog"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// Handler holds what every handler needs to read a request and write a
// response. The store a handler reads is passed to it when it is built, so a
// route's dependencies are visible where the route is registered.
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
	_ *slog.Logger,
	reader *request.Reader,
	store *inventory.Store,
	jw *response.JSONWriter,
) *Handler {
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
	h.mux.HandleFunc("GET /devices/{id}/traffic", jw.Handle(h.deviceTraffic(store)))

	h.mux.HandleFunc("GET /events", jw.Handle(h.listEvents(store)))
	h.mux.HandleFunc("GET /scans", jw.Handle(h.listScans(store)))

	return h
}

// badRequest reports a request the router matched but the handler cannot use,
// such as a path parameter that is not a number.
func badRequest(detail string) error {
	return &request.ReadError{
		HTTPStatusCode: http.StatusBadRequest,
		Message:        detail,
	}
}
