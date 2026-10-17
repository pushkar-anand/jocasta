package web

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// deviceHistoryLimit is how much of the history of one device the detail page
// shows.
const deviceHistoryLimit = 30

// devicesPerPage is how many rows one page of the device list shows. The page
// still reads and sorts the whole list, because SQL cannot order by name or
// address. So this only limits how much of it reaches the DOM at once.
const devicesPerPage = 50

// devicesData is the device list, and the state of its filter form.
type devicesData struct {
	view
	Devices  []*inventory.Device
	Groups   []string
	Networks []*inventory.Network

	// Total is how many devices the filter matched across every page. Devices
	// holds only the shown page. Pager is nil when the match fits on one
	// page.
	Total int
	Pager *listPager

	// The form values, kept as strings. So a template can compare them with
	// the option values, without a conversion. Network is the id of the
	// chosen network, as text. Type is a device class.
	Query          string
	Group          string
	Network        string
	Type           string
	Status         string
	Sort           string
	IncludeIgnored bool
	Watched        bool

	// Page is the 1-based page that the form asked for, before the clamp to
	// the real number of pages.
	Page int

	// Base is the path of this list. It is "/devices" when empty. The network
	// page uses the list again for one prefix, and sets Base to its own path.
	// So the filter form and the pager point to that page.
	Base string

	// OnNetwork is the fixed prefix of the list on the page of a network. The
	// network select is then left out, because the path already names it. The
	// query string has no repeated network id.
	OnNetwork *inventory.Network
}

// listPath is the path of this list.
func (d *devicesData) listPath() string {
	if d.Base != "" {
		return d.Base
	}

	return "/devices"
}

// FilterAction is where the filter form sends without htmx. It is also where
// the "clear" link goes: the list with no filter.
func (d *devicesData) FilterAction() string { return d.listPath() }

// FilterRows is the fragment endpoint that the filter form fetches with htmx.
func (d *devicesData) FilterRows() string { return d.listPath() + "/rows" }

// Filtered reports whether the form limits the list. The fixed network of a
// network page is the page itself. So it does not count.
func (d *devicesData) Filtered() bool {
	chosenNetwork := d.Network != "" && d.OnNetwork == nil

	return d.Query != "" || d.Group != "" || chosenNetwork || d.Type != "" ||
		d.Status != "" || d.Sort != "" || d.IncludeIgnored || d.Watched
}

// Count writes how many devices are in the heading. It is the size of the
// match when a filter is on. Else, it is the size of the inventory.
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

// listPager positions a paginated list: the shown page, the number of pages,
// and the built addresses of the pages on each side. A nil pager is a list
// short enough to show whole. devicesData has the match count. The count line
// reads it, whether the list has pages or not.
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
		Watched:        d.Watched,
	}
}

// networkID reads the id of the chosen network. A value that is not a number is
// no filter, the same as "any". The select only offers ids. So any other value
// was typed into the address by hand.
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

	// On the page of a network, the prefix is the path. A repeat in the query
	// string could only become different from the path.
	if d.OnNetwork != nil {
		q.Del("network")
	}

	if d.IncludeIgnored {
		q.Set("ignored", "1")
	}

	if d.Watched {
		q.Set("watched", "1")
	}

	return q
}

// address is the URL of this list on a given page. Page one has no page
// parameter. So a first page with no filter is only the own path of the
// list.
func (d *devicesData) address(page int) string {
	q := d.params()

	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}

	return withQuery(d.listPath(), q)
}

// canonical is the address of the page that shows this list. The fragment
// endpoint returns it as HX-Push-Url. So the address bar of the browser shows
// an address that a person can reload or share, although only the table was
// fetched.
func (d *devicesData) canonical() string {
	return d.address(d.Page)
}

// deviceRowView is one device, as the row partial renders it. It embeds the
// device, so the partial reads its fields directly. It also tells whether the
// role of this viewer can edit it. The Edit button of the row depends on
// this.
type deviceRowView struct {
	*inventory.Device
	CanWrite bool
}

// Rows pairs each device on the shown page with the edit permission that the
// row partial needs. It reads Role, which the handler copied from the
// session.
func (d *devicesData) Rows() []deviceRowView {
	canWrite := d.Role.CanWrite()

	rows := make([]deviceRowView, len(d.Devices))
	for i, dev := range d.Devices {
		rows[i] = deviceRowView{Device: dev, CanWrite: canWrite}
	}

	return rows
}

// paginate keeps the page of the sorted match that the form asked for. It
// clamps a page number after the end back to the last real page.
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

// deviceForm reads the validated query into the form state of the list.
//
// It drops a value with the right shape that the inventory does not know. An
// example is a device class outside the vocabulary, or a network id that is
// not a number. The form only sends values that it offered. So an unknown
// value came from a hand edit of the address. The rendered form then shows
// what really applied.
func deviceForm(q deviceQuery) *devicesData {
	d := &devicesData{
		Query:          strings.TrimSpace(q.Query),
		Group:          q.Group,
		IncludeIgnored: q.IncludeIgnored,
		Watched:        q.Watched,
		Page:           1,
	}

	// The pager only links to pages that exist. paginate clamps a number after
	// the end. The validator already refused a number below 1.
	if q.Page > 1 {
		d.Page = q.Page
	}

	// The select offers network ids. Any other value came by hand. If kept,
	// the address would claim a filter that did not apply. An id for a network
	// that no longer exists filters to nothing, as an unknown group does.
	if n, err := strconv.ParseInt(q.Network, 10, 64); err == nil && n > 0 {
		d.Network = strconv.FormatInt(n, 10)
	}

	// The select offers the class vocabulary. A value outside it, such as an
	// old free-text type, is no filter.
	if c := classify.Class(q.Type); c.Known() {
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

// buildDevicesData is the /devices list: the whole inventory, with no scope.
func buildDevicesData(ctx context.Context, store *inventory.Store, q deviceQuery) (*devicesData, error) {
	return buildDeviceListData(ctx, store, q, view{Title: "Devices", Section: "Devices"}, "", nil)
}

// buildDeviceListData builds the filtered device list, with pages. base and
// onNetwork are empty for /devices. The network page passes its own path and
// prefix. So the same list, form and pager render for one network.
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
	data.Window = windowWords(store.OnlineWindow())

	// On the page of a network, the path fixes the prefix. So it overrides the
	// query string.
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

	data.Note = latestSweepNote(ctx, store)

	return data, nil
}

// buildDevicePageData is the detail page of one device: its history, the groups
// that the curation form suggests, and what each source claims about it. query
// is the query of the page. It has the period and filter of the traffic
// section.
func buildDevicePageData(
	ctx context.Context, store *inventory.Store, device *inventory.Device, query url.Values,
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

	traffic, err := buildTrafficSection(ctx, store, device.ID,
		trafficWindowKey(query), trafficFilterFrom(query), store.Now())
	if err != nil {
		return nil, err
	}

	lookups, err := buildDNSSection(ctx, store, device.ID, query.Get("dns"), store.Now())
	if err != nil {
		return nil, err
	}

	conn, err := deviceConnection(ctx, store, device.ID)
	if err != nil {
		return nil, err
	}

	data := &curationForm{
		Title:              device.Name(),
		Section:            "Devices",
		Crumb:              &crumb{Label: "Devices", Href: "/devices"},
		Device:             device,
		Groups:             groups,
		Events:             events,
		Claims:             claims,
		LastChecked:        lastSweptAt(ctx, store),
		Ports:              portRows(device.Ports, device.Services),
		PortScanConfigured: portScanConfigured(ctx, store),
		Traffic:            traffic,
		DNS:                lookups,
		Connection:         conn,
	}

	data.Note = latestSweepNote(ctx, store)

	return data, nil
}
