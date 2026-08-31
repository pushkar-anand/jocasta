package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// deviceEventLimit is how much of a device's history the detail response
// carries. The full log is at /events.
const deviceEventLimit = 50

type devicesResponse struct {
	Devices []inventory.Device `json:"devices"`
	Count   int                `json:"count"`
}

type eventsResponse struct {
	Events []inventory.Event `json:"events"`
	Count  int               `json:"count"`
}

// deviceQuery narrows a device list. Fields are matched by their schema tag, so
// the names here are the ones the query string uses.
type deviceQuery struct {
	Q              string `schema:"q"`
	Group          string `schema:"group"`
	Status         string `schema:"status"`
	Sort           string `schema:"sort"`
	IncludeIgnored bool   `schema:"include_ignored"`
}

// Valid implements request.SelfValidator.
//
// A misspelt filter is rejected rather than ignored: silently returning the
// unfiltered list looks like the filter matched everything.
func (q deviceQuery) Valid(context.Context) map[string]string {
	problems := make(map[string]string)

	if !inventory.Status(q.Status).Valid() {
		problems["status"] = "must be one of: online, offline"
	}

	if !inventory.Sort(q.Sort).Valid() {
		problems["sort"] = "must be one of: last_seen, name, address"
	}

	return problems
}

func (q deviceQuery) filter() inventory.DeviceFilter {
	return inventory.DeviceFilter{
		Query:          q.Q,
		Group:          q.Group,
		Status:         inventory.Status(q.Status),
		Sort:           inventory.Sort(q.Sort),
		IncludeIgnored: q.IncludeIgnored,
	}
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) error {
	q, err := h.reader.ReadAndValidateQueryParams[deviceQuery](r)
	if err != nil {
		return err
	}

	devices, err := h.store.ListDevices(r.Context(), q.filter())
	if err != nil {
		return err
	}

	h.jsonWriter.Ok(w, r, devicesResponse{Devices: devices, Count: len(devices)})

	return nil
}

func (h *Handler) getDevice(w http.ResponseWriter, r *http.Request) error {
	id, err := deviceID(r)
	if err != nil {
		return err
	}

	device, err := h.store.GetDevice(r.Context(), id)
	if err != nil {
		return err
	}

	h.jsonWriter.Ok(w, r, device)

	return nil
}

func (h *Handler) deviceEvents(w http.ResponseWriter, r *http.Request) error {
	id, err := deviceID(r)
	if err != nil {
		return err
	}

	// The device is read first so that asking for the history of a device that
	// does not exist is a 404 rather than an empty list.
	if _, err := h.store.GetDevice(r.Context(), id); err != nil {
		return err
	}

	events, err := h.store.DeviceEvents(r.Context(), id, deviceEventLimit)
	if err != nil {
		return err
	}

	h.jsonWriter.Ok(w, r, eventsResponse{Events: events, Count: len(events)})

	return nil
}

func (h *Handler) groups(w http.ResponseWriter, r *http.Request) error {
	groups, err := h.store.Groups(r.Context())
	if err != nil {
		return err
	}

	h.jsonWriter.Ok(w, r, map[string]any{"groups": groups})

	return nil
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) error {
	stats, err := h.store.Stats(r.Context())
	if err != nil {
		return err
	}

	h.jsonWriter.Ok(w, r, stats)

	return nil
}

// deviceID reads the id the route captured. The pattern admits any segment, so
// the handler is where a non-numeric one is turned away.
func deviceID(r *http.Request) (int64, error) {
	raw := r.PathValue("id")

	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, badRequest("Device id must be a positive whole number.")
	}

	return id, nil
}
