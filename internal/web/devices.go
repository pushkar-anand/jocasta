package web

import (
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
	Devices  []*inventory.Device
	Groups   []string
	Networks []*inventory.Network

	// The form values, kept as strings so a template can compare them against
	// the option values without converting anything. Network is the id of the
	// chosen network, as text.
	Query          string
	Group          string
	Network        string
	Status         string
	Sort           string
	IncludeIgnored bool
}

// filter is what the form asked the inventory for.
func (d devicesData) filter() inventory.DeviceFilter {
	return inventory.DeviceFilter{
		Query:          d.Query,
		Group:          d.Group,
		Network:        d.networkID(),
		Status:         inventory.Status(d.Status),
		Sort:           inventory.Sort(d.Sort),
		IncludeIgnored: d.IncludeIgnored,
	}
}

// networkID reads the chosen network's id. A value that is not a number is no
// filter at all, the same as choosing "any": the select only offers ids, so
// anything else was typed into the address by hand.
func (d devicesData) networkID() int64 {
	id, err := strconv.ParseInt(d.Network, 10, 64)
	if err != nil {
		return 0
	}

	return id
}

// canonical is the address of the page showing this list. The fragment endpoint
// returns it as HX-Push-Url, so the browser's address bar ends up somewhere
// that can be reloaded or shared even though only the table was fetched.
func (d devicesData) canonical() string {
	q := make(url.Values)

	for key, value := range map[string]string{
		"q": d.Query, "group": d.Group, "network": d.Network, "status": d.Status, "sort": d.Sort,
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
func deviceForm(q url.Values) *devicesData {
	d := &devicesData{
		Query:          strings.TrimSpace(q.Get("q")),
		Group:          q.Get("group"),
		IncludeIgnored: q.Get("ignored") == "1",
	}

	// The select offers network ids; anything that is not one arrived by hand,
	// and keeping it would leave the address claiming a filter that was not
	// applied. An id for a network that no longer exists is left to filter to
	// nothing, the same as an unknown group does.
	if n, err := strconv.ParseInt(q.Get("network"), 10, 64); err == nil && n > 0 {
		d.Network = strconv.FormatInt(n, 10)
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

func (h *Handler) devicesData(r *http.Request) (*devicesData, error) {
	ctx := r.Context()

	data := deviceForm(r.URL.Query())
	data.view = view{Title: "Devices", Section: "Devices"}

	devices, err := h.store.ListDevices(ctx, data.filter())
	if err != nil {
		return nil, err
	}

	groups, err := h.store.Groups(ctx)
	if err != nil {
		return nil, err
	}

	networks, err := h.store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	data.Devices = devices
	data.Groups = groups
	data.Networks = networks

	if note, err := h.sweepNote(ctx); err == nil {
		data.Note = note
	}

	return data, nil
}

func (h *Handler) device(w http.ResponseWriter, r *http.Request) {
	// A device that is not there is a page that is not there, not a fault.
	device, ok := h.deviceFromPath(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	events, err := h.store.DeviceEvents(ctx, device.ID, deviceHistoryLimit)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	groups, err := h.store.Groups(ctx)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	claims, err := h.store.DeviceSources(ctx, device.ID)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	data := &curationForm{
		view: view{
			Title:   device.Name(),
			Section: "Devices",
			Crumb:   &crumb{Label: "Devices", Href: "/devices"},
		},
		Device: device,
		Groups: groups,
		Events: events,
		Claims: claims,
	}

	if note, err := h.sweepNote(ctx); err == nil {
		data.Note = note
	}

	h.renderer.Render(w, r, "page/device", data)
}
