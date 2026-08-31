package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// rowForm is one device as an editable row, with the groups already in use so
// the group field can suggest them.
type rowForm struct {
	Device inventory.Device
	Groups []string
}

// curationForm is the device panel: its identity, and the form that curates it.
type curationForm struct {
	view
	Device inventory.Device
	Groups []string
	Events []inventory.Event

	// Saved marks the panel as having just been saved, which is the only way a
	// swapped-in fragment can say that anything happened.
	Saved bool
}

// curationFrom reads an edit out of a submitted form.
//
// Every user-owned field is applied, so a form that does not carry one clears
// it. The row form carries the fields it does not show as hidden inputs for
// exactly this reason.
func curationFrom(f url.Values) inventory.Curation {
	return inventory.Curation{
		Label: f.Get("label"),
		Notes: f.Get("notes"),
		Group: f.Get("group"),
		Type:  f.Get("type"),

		// A checkbox submits its value only when it is checked, so anything
		// else means unchecked.
		Ignored: f.Get("ignored") == "1",
	}
}

// deviceRow serves one row as it is displayed, which is how an edit is
// cancelled.
func (h *Handler) deviceRow(w http.ResponseWriter, r *http.Request) {
	device, ok := h.deviceFromPath(w, r)
	if !ok {
		return
	}

	h.renderer.Render(w, r, "partial/device-row", device)
}

// deviceRowEdit serves the same row as a form.
func (h *Handler) deviceRowEdit(w http.ResponseWriter, r *http.Request) {
	device, ok := h.deviceFromPath(w, r)
	if !ok {
		return
	}

	groups, err := h.store.Groups(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.renderer.Render(w, r, "partial/device-row-form", rowForm{Device: device, Groups: groups})
}

// updateDeviceRow applies an edit made from the list and answers with the row.
func (h *Handler) updateDeviceRow(w http.ResponseWriter, r *http.Request) {
	device, ok := h.applyEdit(w, r)
	if !ok {
		return
	}

	h.renderer.Render(w, r, "partial/device-row", device)
}

// updateDevice applies an edit made on the device's own page and answers with
// the panel, which carries the heading a new label changes.
func (h *Handler) updateDevice(w http.ResponseWriter, r *http.Request) {
	device, ok := h.applyEdit(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	groups, err := h.store.Groups(ctx)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	// Read after the write, so the log the response carries includes the edit
	// that was just made.
	events, err := h.store.DeviceEvents(ctx, device.ID, deviceHistoryLimit)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.renderer.Render(w, r, "partial/device-panel", curationForm{
		Device: device,
		Groups: groups,
		Events: events,
		Saved:  true,
	})
}

// applyEdit reads the form and writes it. It reports whether the caller should
// go on to render; a request it turns away has already been answered.
func (h *Handler) applyEdit(w http.ResponseWriter, r *http.Request) (inventory.Device, bool) {
	id, ok := deviceIDFromPath(r)
	if !ok {
		h.notFound(w, r)

		return inventory.Device{}, false
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Could not read the form", http.StatusBadRequest)

		return inventory.Device{}, false
	}

	device, err := h.store.UpdateCuration(r.Context(), id, curationFrom(r.PostForm))
	if err != nil {
		if errors.Is(err, inventory.ErrNotFound) {
			h.notFound(w, r)

			return inventory.Device{}, false
		}

		h.fail(w, r, err)

		return inventory.Device{}, false
	}

	return device, true
}

// deviceFromPath reads the device the route names, answering the request itself
// if there is none.
func (h *Handler) deviceFromPath(w http.ResponseWriter, r *http.Request) (inventory.Device, bool) {
	id, ok := deviceIDFromPath(r)
	if !ok {
		h.notFound(w, r)

		return inventory.Device{}, false
	}

	device, err := h.store.GetDevice(r.Context(), id)
	if err != nil {
		if errors.Is(err, inventory.ErrNotFound) {
			h.notFound(w, r)

			return inventory.Device{}, false
		}

		h.fail(w, r, err)

		return inventory.Device{}, false
	}

	return device, true
}

// deviceIDFromPath reads the id the route captured. The pattern admits any
// segment, so this is where one that is not an id is rejected.
func deviceIDFromPath(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}

	return id, true
}
