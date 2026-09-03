package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// deviceHistoryLimit is how much of one device's history the detail page shows.
const deviceHistoryLimit = 30

// devicesPerPage is how many rows one page of the device list shows. The list
// is still read and sorted whole -- ordering by name or address cannot be done
// in SQL -- so this only bounds how much of it reaches the DOM at once.
const devicesPerPage = 50

// devicesData is the device list and the state of the form that narrowed it.
type devicesData struct {
	view
	Devices  []*inventory.Device
	Groups   []string
	Networks []*inventory.Network

	// Total is how many devices the filter matched across every page; Devices
	// holds only the page being shown. Pager is nil when the match fits on one.
	Total int
	Pager *listPager

	// The form values, kept as strings so a template can compare them against
	// the option values without converting anything. Network is the id of the
	// chosen network, as text; Type is a device class.
	Query          string
	Group          string
	Network        string
	Type           string
	Status         string
	Sort           string
	IncludeIgnored bool

	// Page is the 1-based page the form asked for, before it is clamped to how
	// many there turned out to be.
	Page int

	// Base is where this list lives, "/devices" when empty. The network page
	// reuses the list scoped to one prefix and sets Base to its own path, so
	// the filter form and the pager address that page rather than /devices.
	Base string

	// OnNetwork is the prefix the list is pinned to on a network's page. The
	// network select is left out then, since the path already names it, and
	// the query string carries no redundant network id.
	OnNetwork *inventory.Network
}

// listPath is where this list lives.
func (d devicesData) listPath() string {
	if d.Base != "" {
		return d.Base
	}

	return "/devices"
}

// FilterAction is where the filter form submits without htmx, and also where
// the "clear" link leads: the list with nothing narrowing it.
func (d devicesData) FilterAction() string { return d.listPath() }

// FilterRows is the fragment endpoint the filter form fetches with htmx.
func (d devicesData) FilterRows() string { return d.listPath() + "/rows" }

// Filtered reports whether the form is narrowing the list. The network a
// network page pins is the page, not a filter, so it does not count.
func (d devicesData) Filtered() bool {
	chosenNetwork := d.Network != "" && d.OnNetwork == nil

	return d.Query != "" || d.Group != "" || chosenNetwork || d.Type != "" ||
		d.Status != "" || d.Sort != "" || d.IncludeIgnored
}

// Count words how many devices are in the heading: the size of the match when a
// filter is on, the size of the inventory when it is not.
func (d devicesData) Count() string {
	switch {
	case d.Filtered() && d.Total == 1:
		return "1 match"
	case d.Filtered():
		return strconv.Itoa(d.Total) + " matches"
	case d.Total == 1:
		return "1 device"
	default:
		return strconv.Itoa(d.Total) + " devices"
	}
}

// listPager positions a paginated list: which page is shown, how many there
// are, and the ready-built addresses of the pages either side. A nil one is a
// list short enough to show whole. The match count is carried on devicesData,
// which the count line reads whether or not the list is paged.
type listPager struct {
	Page  int
	Pages int

	prev string
	next string
}

func (p *listPager) HasPrev() bool    { return p.Page > 1 }
func (p *listPager) HasNext() bool    { return p.Page < p.Pages }
func (p *listPager) PrevHref() string { return p.prev }
func (p *listPager) NextHref() string { return p.next }

// filter is what the form asked the inventory for.
func (d devicesData) filter() inventory.DeviceFilter {
	return inventory.DeviceFilter{
		Query:          d.Query,
		Group:          d.Group,
		Network:        d.networkID(),
		Type:           classify.Class(d.Type),
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

// params is the filter as query values, without the page.
func (d devicesData) params() url.Values {
	q := make(url.Values)

	for key, value := range map[string]string{
		"q": d.Query, "group": d.Group, "network": d.Network,
		"type": d.Type, "status": d.Status, "sort": d.Sort,
	} {
		if value != "" {
			q.Set(key, value)
		}
	}

	// On a network's page the prefix is the path, so repeating it in the query
	// string would only be there to fall out of step with the path.
	if d.OnNetwork != nil {
		q.Del("network")
	}

	if d.IncludeIgnored {
		q.Set("ignored", "1")
	}

	return q
}

// address is the URL of this list on a given page. Page one carries no page
// parameter, so an unfiltered first page is just the list's own path.
func (d devicesData) address(page int) string {
	q := d.params()

	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}

	if len(q) == 0 {
		return d.listPath()
	}

	return d.listPath() + "?" + q.Encode()
}

// canonical is the address of the page showing this list. The fragment endpoint
// returns it as HX-Push-Url, so the browser's address bar ends up somewhere
// that can be reloaded or shared even though only the table was fetched.
func (d devicesData) canonical() string {
	return d.address(d.Page)
}

// paginate keeps the page of the sorted match that the form asked for, clamping
// a page number past the end back to the last real one.
func (d *devicesData) paginate(all []*inventory.Device) {
	d.Total = len(all)

	pages := 1
	if d.Total > devicesPerPage {
		pages = (d.Total + devicesPerPage - 1) / devicesPerPage
	}

	d.Page = min(max(d.Page, 1), pages)

	start := (d.Page - 1) * devicesPerPage
	d.Devices = all[start:min(start+devicesPerPage, d.Total)]

	if pages > 1 {
		d.Pager = &listPager{
			Page:  d.Page,
			Pages: pages,
			prev:  d.address(d.Page - 1),
			next:  d.address(d.Page + 1),
		}
	}
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
		Page:           1,
	}

	// The pager only ever links to pages that exist; a number past the end, or
	// one that is not a number, is read as the first page.
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 1 {
		d.Page = p
	}

	// The select offers network ids; anything that is not one arrived by hand,
	// and keeping it would leave the address claiming a filter that was not
	// applied. An id for a network that no longer exists is left to filter to
	// nothing, the same as an unknown group does.
	if n, err := strconv.ParseInt(q.Get("network"), 10, 64); err == nil && n > 0 {
		d.Network = strconv.FormatInt(n, 10)
	}

	// The select offers the class vocabulary; a value outside it -- including a
	// free-text type from before the field was a fixed list -- is no filter.
	if c := classify.Class(q.Get("type")); c != classify.Unknown && c.Valid() {
		d.Type = string(c)
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
	return h.deviceListData(r, view{Title: "Devices", Section: "Devices"}, "", nil)
}

// deviceListData builds the filtered, paged device list. base and onNetwork are
// empty for /devices; the network page passes its own path and prefix so the
// same list, form and pager render scoped to one network.
func (h *Handler) deviceListData(
	r *http.Request, v view, base string, onNetwork *inventory.Network,
) (*devicesData, error) {
	ctx := r.Context()

	data := deviceForm(r.URL.Query())
	data.view = v
	data.Base = base
	data.OnNetwork = onNetwork

	// On a network's page the prefix is the path, not a choice, so it is forced
	// past whatever the query string asked for.
	if onNetwork != nil {
		data.Network = strconv.FormatInt(onNetwork.ID, 10)
	}

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

	data.Groups = groups
	data.Networks = networks
	data.paginate(devices)

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
		Device:      device,
		Groups:      groups,
		Events:      events,
		Claims:      claims,
		LastChecked: h.lastSweptAt(ctx),
	}

	if note, err := h.sweepNote(ctx); err == nil {
		data.Note = note
	}

	h.renderer.Render(w, r, "page/device", data)
}
