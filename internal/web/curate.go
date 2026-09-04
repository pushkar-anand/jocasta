package web

import (
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// rowForm is one device as an editable row, with the groups already in use so
// the group field can suggest them.
type rowForm struct {
	Device *inventory.Device
	Groups []string
}

// curationForm is the device panel: its identity, and the form that curates it.
type curationForm struct {
	view
	Device *inventory.Device
	Groups []string
	Events []*inventory.Event

	// LastChecked is when a sweep last ran, whether or not it found this
	// device. Read beside the device's own last_seen it says whether a stale
	// sighting means the device has gone or the sweeps have. Zero before the
	// first sweep.
	LastChecked time.Time

	// Claims is what each source says about the device. Only the full page
	// fills it: curating a device changes nothing any source claims, so the
	// panel swap has nothing to say about them.
	Claims []*inventory.Claim

	// Saved marks the panel as having just been saved, which is the only way a
	// swapped-in fragment can say that anything happened.
	Saved bool
}

// deviceEdit is what a caller may change on a device through the row or panel
// form -- the same shape curationRequest takes as JSON, in form fields instead
// of a body. Every field is applied, so a form that does not carry one clears
// it; the row form carries the fields it does not show as hidden inputs for
// exactly this reason.
type deviceEdit struct {
	Label   string `schema:"label" validate:"omitempty,max=200"`
	Notes   string `schema:"notes" validate:"omitempty,max=2000"`
	Group   string `schema:"group" validate:"omitempty,max=100"`
	Type    string `schema:"type" validate:"omitempty,deviceclass"`
	Ignored bool   `schema:"ignored"`
}

func (e deviceEdit) toCuration() inventory.Curation {
	return inventory.Curation{
		Label:   e.Label,
		Notes:   e.Notes,
		Group:   e.Group,
		Type:    e.Type,
		Ignored: e.Ignored,
	}
}

// deviceRow serves one row as it is displayed, which is how an edit is
// cancelled.
func (h *Handler) deviceRow() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		device, err := h.store.Device(r.Context(), id)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialDeviceRow, device)
		return nil
	}
}

// deviceRowForm serves the same row as an editable form.
func (h *Handler) deviceRowForm() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		device, err := h.store.Device(r.Context(), id)
		if err != nil {
			return err
		}

		groups, err := h.store.Groups(r.Context())
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialDeviceRowForm, &rowForm{Device: device, Groups: groups})
		return nil
	}
}

// updateDeviceRow applies an edit made from the list and answers with the row.
func (h *Handler) updateDeviceRow() response.HandlerFunc {

	type form struct {
		deviceEdit
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		data, err := h.reader.ReadAndValidateForm[form](r)
		if err != nil {
			return err
		}

		device, err := h.store.UpdateCuration(r.Context(), id, data.deviceEdit.toCuration())
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialDeviceRow, device)

		return nil
	}
}

// updateDevice applies an edit made on the device's own page and answers with
// the panel, which carries the heading a new label changes.
func (h *Handler) updateDevice() response.HandlerFunc {

	type form struct {
		deviceEdit
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		data, err := h.reader.ReadAndValidateForm[form](r)
		if err != nil {
			return err
		}

		device, err := h.store.UpdateCuration(r.Context(), id, data.deviceEdit.toCuration())
		if err != nil {
			return err
		}

		groups, err := h.store.Groups(ctx)
		if err != nil {
			return err
		}

		// Read after the write, so the log the response carries includes the edit
		// that was just made.
		events, err := h.store.DeviceEvents(ctx, device.ID, deviceHistoryLimit)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialDevicePanel, &curationForm{
			Device:      device,
			Groups:      groups,
			Events:      events,
			LastChecked: lastSweptAt(ctx, h.store),
			Saved:       true,
		})
		return nil
	}
}
