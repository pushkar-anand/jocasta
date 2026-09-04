package web

import (
	"context"
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
func (d *devicesData) listPath() string {
	if d.Base != "" {
		return d.Base
	}

	return "/devices"
}

// FilterAction is where the filter form submits without htmx, and also where
// the "clear" link leads: the list with nothing narrowing it.
func (d *devicesData) FilterAction() string { return d.listPath() }

// FilterRows is the fragment endpoint the filter form fetches with htmx.
func (d *devicesData) FilterRows() string { return d.listPath() + "/rows" }

// Filtered reports whether the form is narrowing the list. The network a
// network page pins is the page, not a filter, so it does not count.
func (d *devicesData) Filtered() bool {
	chosenNetwork := d.Network != "" && d.OnNetwork == nil

	return d.Query != "" || d.Group != "" || chosenNetwork || d.Type != "" ||
		d.Status != "" || d.Sort != "" || d.IncludeIgnored
}

// Count words how many devices are in the heading: the size of the match when a
// filter is on, the size of the inventory when it is not.
func (d *devicesData) Count() string {
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
func (d *devicesData) filter() inventory.DeviceFilter {
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
func (d *devicesData) networkID() int64 {
	id, err := strconv.ParseInt(d.Network, 10, 64)
	if err != nil {
		return 0
	}

	return id
}

// params is the filter as query values, without the page.
func (d *devicesData) params() url.Values {
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
func (d *devicesData) address(page int) string {
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
func (d *devicesData) canonical() string {
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

// deviceForm reads the validated query into the list's form state.
//
// A value whose shape is right but that the inventory does not recognise -- a
// device class that is not in the vocabulary, a network id that is not a
// number -- is dropped rather than refused. The form only ever submits values
// it offered, so an unrecognised one arrived by hand-editing the address, and
// the rendered form then shows what was actually applied.
func deviceForm(q deviceQuery) *devicesData {
	d := &devicesData{
		Query:          strings.TrimSpace(q.Query),
		Group:          q.Group,
		IncludeIgnored: q.IncludeIgnored,
		Page:           1,
	}

	// The pager only ever links to pages that exist; a number past the end is
	// clamped back by paginate, and the validator has already turned away one
	// below 1.
	if q.Page > 1 {
		d.Page = q.Page
	}

	// The select offers network ids; anything that is not one arrived by hand,
	// and keeping it would leave the address claiming a filter that was not
	// applied. An id for a network that no longer exists is left to filter to
	// nothing, the same as an unknown group does.
	if n, err := strconv.ParseInt(q.Network, 10, 64); err == nil && n > 0 {
		d.Network = strconv.FormatInt(n, 10)
	}

	// The select offers the class vocabulary; a value outside it -- including a
	// free-text type from before the field was a fixed list -- is no filter.
	if c := classify.Class(q.Type); c != classify.Unknown && c.Valid() {
		d.Type = string(c)
	}

	if s := inventory.Status(q.Status); s.Valid() {
		d.Status = string(s)
	}

	if by := inventory.Sort(q.Sort); by.Valid() {
		d.Sort = string(by)
	}

	return d
}

// buildDevicesData is the /devices list: the whole inventory, unscoped.
func buildDevicesData(ctx context.Context, store *inventory.Store, q deviceQuery) (*devicesData, error) {
	return buildDeviceListData(ctx, store, q, view{Title: "Devices", Section: "Devices"}, "", nil)
}

// buildDeviceListData builds the filtered, paged device list. base and onNetwork
// are empty for /devices; the network page passes its own path and prefix so the
// same list, form and pager render scoped to one network.
func buildDeviceListData(
	ctx context.Context,
	store *inventory.Store,
	q deviceQuery,
	v view,
	base string,
	onNetwork *inventory.Network,
) (*devicesData, error) {
	data := deviceForm(q)
	data.view = v
	data.Base = base
	data.OnNetwork = onNetwork

	// On a network's page the prefix is the path, not a choice, so it is forced
	// past whatever the query string asked for.
	if onNetwork != nil {
		data.Network = strconv.FormatInt(onNetwork.ID, 10)
	}

	devices, err := store.ListDevices(ctx, data.filter())
	if err != nil {
		return nil, err
	}

	groups, err := store.Groups(ctx)
	if err != nil {
		return nil, err
	}

	networks, err := store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	data.Groups = groups
	data.Networks = networks
	data.paginate(devices)

	// A first run has no sweep behind it, which is a state to render rather than
	// a failure to report.
	if scan, err := store.LatestScan(ctx); err == nil {
		data.Note = sweepNote(scan)
	}

	return data, nil
}

// buildDevicePageData is one device's detail page: its history, the groups the
// curation form suggests, and what each source claims about it.
func buildDevicePageData(
	ctx context.Context, store *inventory.Store, device *inventory.Device,
) (*curationForm, error) {
	events, err := store.DeviceEvents(ctx, device.ID, deviceHistoryLimit)
	if err != nil {
		return nil, err
	}

	groups, err := store.Groups(ctx)
	if err != nil {
		return nil, err
	}

	claims, err := store.DeviceSources(ctx, device.ID)
	if err != nil {
		return nil, err
	}

	data := &curationForm{
		Title:       device.Name(),
		Section:     "Devices",
		Crumb:       &crumb{Label: "Devices", Href: "/devices"},
		Device:      device,
		Groups:      groups,
		Events:      events,
		Claims:      claims,
		LastChecked: lastSweptAt(ctx, store),
	}

	if scan, err := store.LatestScan(ctx); err == nil {
		data.Note = sweepNote(scan)
	}

	return data, nil
}
