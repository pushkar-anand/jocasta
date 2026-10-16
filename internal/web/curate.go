package web

import (
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// rowForm is one device as an editable row, with the groups in use. So the
// group field can suggest them.
type rowForm struct {
	Device *inventory.Device
	Groups []string
}

// curationForm is the device panel: its identity, and the form for its
// curation.
type curationForm struct {
	view
	Device *inventory.Device
	Groups []string
	Events []*inventory.Event

	// LastChecked is when a sweep last ran, whether it found this device or
	// not. With the last_seen of the device, it tells whether an old sighting
	// means that the device left, or that the sweeps stopped. It is zero
	// before the first sweep.
	LastChecked time.Time

	// Claims is what each source says about the device. Only the full page
	// fills it. A curation changes nothing that a source claims. So the panel
	// swap has nothing to show about them.
	Claims []*inventory.Claim

	// Ports are the rows of the Ports section: the ports that a scan recorded,
	// and the services that the device advertised, merged by port. Only the
	// full page fills it.
	Ports []*portRow

	// PortScanConfigured tells whether a port scan ever finished. So the
	// Ports section can tell "nothing is open" from "nothing checked". Only
	// the full page fills it.
	PortScanConfigured bool

	// Traffic is who the device talked to over the period of the page. Only
	// the full page fills it.
	Traffic *trafficSection

	// DNS is what the device looked up over the period of the page. Only the
	// full page fills it.
	DNS *dnsSection

	// Connection is where the device is in the tree of the network. Only the
	// full page fills it, and only after the read of a source.
	Connection *connection

	// Saved marks the panel as just saved. Only in this way can a swapped-in
	// fragment tell that something happened.
	Saved bool

	// Notice is the saved message when the save was not an edit of the
	// details, such as a watch of the device. Empty means that the details
	// saved.
	Notice string
}

// watchForm tells whether to watch the device, from the Watch and Stop
// watching buttons of the panel.
type watchForm struct {
	Watched bool `schema:"watched"`
}

// deviceEdit is what a caller can change on a device through the row or panel
// form. It has the same shape as the JSON of curationRequest, in form fields.
// Every field applies. So a form without a field clears it. For this reason,
// the row form has the fields that it does not show as hidden inputs.
type deviceEdit struct {
	Label   string `schema:"label" validate:"omitempty,max=200"`
	Notes   string `schema:"notes" validate:"omitempty,max=2000"`
	Group   string `schema:"group" validate:"omitempty,max=100"`
	Type    string `schema:"type" validate:"omitempty,deviceclass"`
	Ignored bool   `schema:"ignored"`
}

func (e deviceEdit) toCuration() inventory.Curation { return inventory.Curation(e) }

// deviceRow serves one row, as it is displayed. This cancels an edit.
func (h *Handler) deviceRow(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		device, err := h.store.Device(r.Context(), id)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialDeviceRow, deviceRowView{
			Device:   device,
			CanWrite: sm.CurrentRole(r.Context()).CanWrite(),
		})

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

// updateDeviceRow applies an edit from the list, and answers with the row. Only
// a writer can use the route. So the row in the answer always has the Edit
// button.
func (h *Handler) updateDeviceRow(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		data, err := h.reader.ReadAndValidateForm[deviceEdit](r)
		if err != nil {
			return err
		}

		device, err := h.store.UpdateCuration(r.Context(), id, data.toCuration())
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialDeviceRow, deviceRowView{
			Device:   device,
			CanWrite: sm.CurrentRole(r.Context()).CanWrite(),
		})

		return nil
	}
}

// updateDevice applies an edit from the page of the device, and answers with
// the panel. The panel has the heading that a new label changes.
func (h *Handler) updateDevice(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		data, err := h.reader.ReadAndValidateForm[deviceEdit](r)
		if err != nil {
			return err
		}

		device, err := h.store.UpdateCuration(r.Context(), id, data.toCuration())
		if err != nil {
			return err
		}

		return h.savedPanel(w, r, sm, device, "")
	}
}

// watchDevice starts or stops the watch of a device, from its own page. It
// answers with the panel. The panel shows the change, and has the button that
// undoes it.
func (h *Handler) watchDevice(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		data, err := h.reader.ReadAndValidateForm[watchForm](r)
		if err != nil {
			return err
		}

		device, err := h.store.Watch(r.Context(), id, data.Watched)
		if err != nil {
			return err
		}

		notice := "Stopped watching " + device.Name() + "."
		if device.Watched {
			notice = "Watching " + device.Name() + "."
		}

		return h.savedPanel(w, r, sm, device, notice)
	}
}

// savedPanel answers a change on the page of the device with the panel, marked
// as saved. It also answers with the log, which now includes the change.
func (h *Handler) savedPanel(
	w http.ResponseWriter,
	r *http.Request,
	sm *auth.Session,
	device *inventory.Device,
	notice string,
) error {
	ctx := r.Context()

	groups, err := h.store.Groups(ctx)
	if err != nil {
		return err
	}

	// Read after the write. So the log in the response includes the new
	// edit.
	events, err := h.store.DeviceEvents(ctx, device.ID, deviceHistoryLimit)
	if err != nil {
		return err
	}

	panel := &curationForm{
		Device:      device,
		Groups:      groups,
		Events:      events,
		LastChecked: lastSweptAt(ctx, h.store),
		Saved:       true,
		Notice:      notice,
		Role:        sm.CurrentRole(ctx),
	}

	h.htmlWriter.Success(w, r, templatePartialDevicePanel, panel)

	return nil
}
