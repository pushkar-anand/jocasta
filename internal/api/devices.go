package api

import (
	"net/http"
	"strconv"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// deviceEventLimit is how much of a device's history the detail response
// carries. The full log is at /events.
const deviceEventLimit = 50

func (h *Handler) listDevices(store *inventory.Store) response.HandlerFunc {
	type (
		// devicesRequest narrows a device list. Fields are matched by their
		// schema tag, so the names here are the ones the query string uses.
		//
		// A misspelt filter is rejected rather than ignored: silently returning
		// the unfiltered list looks like the filter matched everything. The
		// values each rule admits are the ones inventory.Status and
		// inventory.Sort name.
		devicesRequest struct {
			Q              string `schema:"q"`
			Group          string `schema:"group"`
			Status         string `schema:"status" validate:"omitempty,oneof=online offline"`
			Sort           string `schema:"sort" validate:"omitempty,oneof=last_seen name address"`
			IncludeIgnored bool   `schema:"include_ignored"`
		}

		devicesResponse struct {
			Devices []inventory.Device `json:"devices"`
			Count   int                `json:"count"`
		}
	)

	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[devicesRequest](r)
		if err != nil {
			return err
		}

		devices, err := store.ListDevices(r.Context(), inventory.DeviceFilter{
			Query:          q.Q,
			Group:          q.Group,
			Status:         inventory.Status(q.Status),
			Sort:           inventory.Sort(q.Sort),
			IncludeIgnored: q.IncludeIgnored,
		})
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, devicesResponse{Devices: devices, Count: len(devices)})

		return nil
	}
}

func (h *Handler) getDevice(store *inventory.Store) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := deviceID(r)
		if err != nil {
			return err
		}

		device, err := store.GetDevice(r.Context(), id)
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, device)

		return nil
	}
}

func (h *Handler) deviceEvents(store *inventory.Store) response.HandlerFunc {
	type eventsResponse struct {
		Events []inventory.Event `json:"events"`
		Count  int               `json:"count"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := deviceID(r)
		if err != nil {
			return err
		}

		// The device is read first so that asking for the history of a device
		// that does not exist is a 404 rather than an empty list.
		if _, err := store.GetDevice(r.Context(), id); err != nil {
			return err
		}

		events, err := store.DeviceEvents(r.Context(), id, deviceEventLimit)
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, eventsResponse{Events: events, Count: len(events)})

		return nil
	}
}

func (h *Handler) groups(store *inventory.Store) response.HandlerFunc {
	type groupsResponse struct {
		Groups []string `json:"groups"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		groups, err := store.Groups(r.Context())
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, groupsResponse{Groups: groups})

		return nil
	}
}

func (h *Handler) stats(store *inventory.Store) response.HandlerFunc {
	// The counts are the whole response, so the wrapper only exists to name
	// what this route returns; the fields are inlined by the embedding.
	type statsResponse struct {
		inventory.Stats
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		stats, err := store.Stats(r.Context())
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, statsResponse{Stats: stats})

		return nil
	}
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
