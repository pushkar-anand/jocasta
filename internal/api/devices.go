package api

import (
	"net/http"
	"strconv"

	"github.com/go-playground/validator/v10"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// DeviceClassRule is the validation rule of the "deviceclass" tag. A type must
// name a class that the classifier knows, or be empty to keep the guess. A
// type outside the closed set cannot render, so the rule refuses it. The
// server registers the tag on its validator at startup.
func DeviceClassRule(fl validator.FieldLevel) bool {
	return classify.Class(fl.Field().String()).Valid()
}

// deviceEventLimit is how much of the history of a device the detail response
// has. The full log is at /events.
const deviceEventLimit = 50

// listDevices serves recorded devices that match the query filters. It leaves
// out ignored devices, unless include_ignored is set.
func (h *Handler) listDevices(store *inventory.Store) response.HandlerFunc {
	type (
		// devicesRequest limits a device list. Fields match by their schema
		// tag. So the names here are the names in the query string.
		//
		// The handler refuses a misspelt filter. The full list with no warning
		// would look like a filter that matched everything. Each rule allows
		// the values that inventory.Status and inventory.Sort name.
		devicesRequest struct {
			Q              string `schema:"q"`
			Group          string `schema:"group"`
			Status         string `schema:"status" validate:"omitempty,oneof=online offline"`
			Sort           string `schema:"sort" validate:"omitempty,oneof=last_seen name address type"`
			IncludeIgnored bool   `schema:"include_ignored"`
			Watched        bool   `schema:"watched"`
		}

		devicesResponse struct {
			Devices []*inventory.Device `json:"devices"`
			Count   int                 `json:"count"`
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
			Watched:        q.Watched,
		})
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, devicesResponse{Devices: devices, Count: len(devices)})

		return nil
	}
}

// getDevice serves the identity, curation, addresses, ports and sources of one
// device. For a missing device, it returns not found.
func (h *Handler) getDevice(store *inventory.Store) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := deviceID(r)
		if err != nil {
			return err
		}

		device, err := store.Device(r.Context(), id)
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, device)

		return nil
	}
}

// updateDevice changes the owner fields in the body, and returns the updated
// device. It does not change the network facts that scans recorded.
func (h *Handler) updateDevice(store *inventory.Store) response.HandlerFunc {
	// curationRequest is what a caller can change on a device. A field that
	// is not in the body keeps its value. A field sent as "" is cleared.
	//
	// No field that a scan writes is here. An address, a vendor or a hardware
	// address is what the network reported, and it stays as reported.
	type curationRequest struct {
		Label   *string `json:"label" validate:"omitempty,max=200"`
		Notes   *string `json:"notes" validate:"omitempty,max=2000"`
		Group   *string `json:"group" validate:"omitempty,max=100"`
		Type    *string `json:"type" validate:"omitempty,deviceclass"`
		Ignored *bool   `json:"ignored"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := deviceID(r)
		if err != nil {
			return err
		}

		body, err := h.reader.ReadAndValidateJSON[curationRequest](r)
		if err != nil {
			return err
		}

		device, err := store.PatchCuration(r.Context(), id, inventory.CurationPatch(*body))
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, device)

		return nil
	}
}

// watchDevice sets whether the owner gets told when a device goes quiet or
// comes back. It returns the device. It is separate from updateDevice, which
// replaces every field in its body. So a client from before the watch feature
// cannot clear it.
func (h *Handler) watchDevice(store *inventory.Store) response.HandlerFunc {
	// watchRequest must give the value. A body without it is a mistake. If
	// the handler read it as false, the watch would stop.
	type watchRequest struct {
		Watched *bool `json:"watched" validate:"required"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := deviceID(r)
		if err != nil {
			return err
		}

		body, err := h.reader.ReadAndValidateJSON[watchRequest](r)
		if err != nil {
			return err
		}

		device, err := store.Watch(r.Context(), id, *body.Watched)
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, device)

		return nil
	}
}

// deviceEvents serves up to deviceEventLimit changes for an existing device.
// For a missing device, it returns not found, not an empty history.
func (h *Handler) deviceEvents(store *inventory.Store) response.HandlerFunc {
	type eventsResponse struct {
		Events []*inventory.Event `json:"events"`
		Count  int                `json:"count"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := deviceID(r)
		if err != nil {
			return err
		}

		// Read the device first. Then a request for the history of a device
		// that does not exist is a 404.
		if err := store.RequireDevice(r.Context(), id); err != nil {
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

// groups serves the group names that the owner gave, for device filters.
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

// stats serves inventory counts, including ignored devices.
func (h *Handler) stats(store *inventory.Store) response.HandlerFunc {
	// The counts are the whole response. The wrapper only names what this
	// route returns. The embedding inlines the fields.
	type statsResponse struct {
		*inventory.Stats
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

// deviceID reads the id that the route captured. The pattern allows any
// segment. So the handler must refuse an id that is not a number.
func deviceID(r *http.Request) (int64, error) {
	raw := r.PathValue("id")

	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, badRequest("Device id must be a positive whole number.")
	}

	return id, nil
}
