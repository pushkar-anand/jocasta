package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// deviceHistoryLimit is how much of one device's history the detail page shows.
const deviceHistoryLimit = 30

// devicesData is the device list and the state of the form that narrowed it.
type devicesData struct {
	view
	Devices []inventory.Device
	Groups  []string

	// The form values, kept as strings so a template can compare them against
	// the option values without converting anything.
	Query          string
	Group          string
	Status         string
	Sort           string
	IncludeIgnored bool
}

// filter is what the form asked the inventory for.
func (d devicesData) filter() inventory.DeviceFilter {
	return inventory.DeviceFilter{
		Query:          d.Query,
		Group:          d.Group,
		Status:         inventory.Status(d.Status),
		Sort:           inventory.Sort(d.Sort),
		IncludeIgnored: d.IncludeIgnored,
	}
}

// canonical is the address of the page showing this list. The fragment endpoint
// returns it as HX-Push-Url, so the browser's address bar ends up somewhere
// that can be reloaded or shared even though only the table was fetched.
func (d devicesData) canonical() string {
	q := make(url.Values)

	for key, value := range map[string]string{
		"q": d.Query, "group": d.Group, "status": d.Status, "sort": d.Sort,
	} {
		if value != "" {
			q.Set(key, value)
		}
	}

	if d.IncludeIgnored {
		q.Set("ignored", "1")
	}

	if len(q) == 0 {
		return "/devices"
	}

	return "/devices?" + q.Encode()
}

// deviceForm reads the form out of the query string.
//
// A value the inventory does not recognise is dropped rather than refused. The
// form only ever submits values it offered, so an unrecognised one arrived by
// hand-editing the address, and the rendered form then shows what was actually
// applied.
func deviceForm(q url.Values) devicesData {
	d := devicesData{
		Query:          strings.TrimSpace(q.Get("q")),
		Group:          q.Get("group"),
		IncludeIgnored: q.Get("ignored") == "1",
	}

	if s := inventory.Status(q.Get("status")); s.Valid() {
		d.Status = string(s)
	}

	if by := inventory.Sort(q.Get("sort")); by.Valid() {
		d.Sort = string(by)
	}

	return d
}

func (h *Handler) devices(w http.ResponseWriter, r *http.Request) {
	data, err := h.devicesData(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.renderer.Render(w, r, "page/devices", data)
}

// deviceRows serves the table on its own, which is what the form fetches as it
// is filled in.
func (h *Handler) deviceRows(w http.ResponseWriter, r *http.Request) {
	data, err := h.devicesData(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	w.Header().Set("HX-Push-Url", data.canonical())

	h.renderer.Render(w, r, "partial/device-rows", data)
}

func (h *Handler) devicesData(r *http.Request) (devicesData, error) {
	ctx := r.Context()

	data := deviceForm(r.URL.Query())
	data.view = view{Title: "Devices", Section: "Devices"}

	devices, err := h.store.ListDevices(ctx, data.filter())
	if err != nil {
		return devicesData{}, err
	}

	groups, err := h.store.Groups(ctx)
	if err != nil {
		return devicesData{}, err
	}

	data.Devices = devices
	data.Groups = groups

	if note, err := h.sweepNote(ctx); err == nil {
		data.Note = note
	}

	return data, nil
}

// deviceData is one device and what has happened to it.
type deviceData struct {
	view
	Device inventory.Device
	Events []inventory.Event
}

func (h *Handler) device(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		h.notFound(w, r)

		return
	}

	ctx := r.Context()

	device, err := h.store.GetDevice(ctx, id)
	if err != nil {
		// A device that is not there is a page that is not there, not a fault.
		if errors.Is(err, inventory.ErrNotFound) {
			h.notFound(w, r)

			return
		}

		h.fail(w, r, err)

		return
	}

	events, err := h.store.DeviceEvents(ctx, id, deviceHistoryLimit)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	data := deviceData{
		view:   view{Title: device.Name(), Section: "Devices"},
		Device: device,
		Events: events,
	}

	if note, err := h.sweepNote(ctx); err == nil {
		data.Note = note
	}

	h.renderer.Render(w, r, "page/device", data)
}
