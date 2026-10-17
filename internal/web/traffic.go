package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/pkg/asn"
)

// trafficWindow is one period the traffic view can cover.
type trafficWindow struct {
	Key   string
	Label string
	span  time.Duration
}

// trafficWindows are the offered periods, shortest first. The first is the
// default, because most visits ask about "today, roughly".
var trafficWindows = []trafficWindow{
	{Key: "24h", Label: "24 hours", span: 24 * time.Hour},
	{Key: "7d", Label: "7 days", span: 7 * 24 * time.Hour},
	{Key: "30d", Label: "30 days", span: 30 * 24 * time.Hour},
}

// windowFor returns the window that key names. For any other value, it returns
// the default, because the key only comes from a link.
func windowFor(key string) trafficWindow {
	for _, w := range trafficWindows {
		if w.Key == key {
			return w
		}
	}

	return trafficWindows[0]
}

// trafficSection is the "Talks to" section of a device, on the page and as the
// fragment that its filter form fetches.
type trafficSection struct {
	DeviceID int64
	Window   trafficWindow
	Windows  []trafficWindow
	Filter   trafficFilter

	// Services are the services that the device used over the window, for
	// the choices of the filter. They come before the filter. So a choice
	// never empties its own list.
	Services []serviceChoice

	// Recorded tells whether Jocasta recorded any traffic. So the section can
	// tell "this device exchanged nothing" from "nothing collects".
	Recorded bool
	Traffic  *inventory.DeviceTraffic

	// Summary is a short summary of the window, before the filter.
	Summary trafficSummary

	// Tabs split what is left after the filter by network segment. Tab is the
	// shown tab.
	Tabs []*trafficTab
	Tab  *trafficTab

	// HasAttempts tells whether the device tried something that never carried
	// data, shown or not. Probing is set when the tries add up to probing of
	// the network.
	HasAttempts bool
	Probing     *inventory.Prober

	// HasBroadcasts tells whether the device sent something to everyone. If
	// so, it gets a Broadcasts tab.
	HasBroadcasts bool

	// Probed is what the internet tried on the device: on its own address,
	// and, for the router, through its outside address.
	Probed []*inventory.Probed
}

// Attribution gives the credit for the organisation names, with the text of
// the traffic page, as their licence requires.
func (*trafficSection) Attribution() string { return asn.Attribution }

// Empty reports whether the device did not talk or try anything in the
// window.
func (t *trafficSection) Empty() bool {
	return t.Traffic.Empty() && !t.HasAttempts && !t.HasBroadcasts
}

// query is the address of the section, with f instead of its filter.
func (t *trafficSection) query(f trafficFilter) url.Values {
	return trafficQuery(t.Window, f)
}

// trafficQuery is the traffic period and filter as query parameters. It leaves
// out each default value.
func trafficQuery(w trafficWindow, f trafficFilter) url.Values {
	q := url.Values{}
	if w.Key != trafficWindows[0].Key {
		q.Set("traffic", w.Key)
	}

	f.encode(q)

	return q
}

// TabPath is the device page on the tab key, with the filter.
func (t *trafficSection) TabPath(key string) string {
	f := t.Filter
	f.Tab = key

	return devicePath(t.DeviceID, t.Window, f)
}

// TabFragment is only the section on the tab key, for htmx.
func (t *trafficSection) TabFragment(key string) string {
	f := t.Filter
	f.Tab = key

	return fragmentPath(t.DeviceID, t.query(f))
}

// TriedPath and TriedFragment change whether tries show, on the same tab.
func (t *trafficSection) TriedPath() string {
	f := t.Filter
	f.Tab, f.Tried = t.Tab.Key, !f.Tried

	return devicePath(t.DeviceID, t.Window, f)
}

// TriedFragment is TriedPath for htmx.
func (t *trafficSection) TriedFragment() string {
	f := t.Filter
	f.Tab, f.Tried = t.Tab.Key, !f.Tried

	return fragmentPath(t.DeviceID, t.query(f))
}

// ClearPath is the device page. It keeps the period, tab and tries switch. It
// drops the search and service.
func (t *trafficSection) ClearPath() string {
	return devicePath(t.DeviceID, t.Window, trafficFilter{Tab: t.Tab.Key, Tried: t.Filter.Tried})
}

// ClearFragment is ClearPath for htmx.
func (t *trafficSection) ClearFragment() string {
	return fragmentPath(t.DeviceID, t.query(trafficFilter{Tab: t.Tab.Key, Tried: t.Filter.Tried}))
}

func fragmentPath(id int64, q url.Values) string {
	return withQuery("/devices/"+strconv.FormatInt(id, 10)+"/traffic", q)
}

// buildTrafficSection reads the traffic of one device over the window that key
// names, limited by f.
func buildTrafficSection(
	ctx context.Context, store *inventory.Store, id int64, key string, f trafficFilter, now time.Time,
) (*trafficSection, error) {
	w := windowFor(key)
	since := now.Add(-w.span)

	recorded, err := store.TrafficRecorded(ctx)
	if err != nil {
		return nil, err
	}

	traffic, err := store.DeviceTraffic(ctx, id, since)
	if err != nil {
		return nil, err
	}

	attempts, err := store.DeviceAttempts(ctx, id, since)
	if err != nil {
		return nil, err
	}

	nets, err := store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	probing, err := store.DeviceProbing(ctx, id, since)
	if err != nil {
		return nil, err
	}

	broadcasts, err := store.DeviceBroadcasts(ctx, id, since)
	if err != nil {
		return nil, err
	}

	probed, err := store.DeviceProbed(ctx, id, since)
	if err != nil {
		return nil, err
	}

	sec := &trafficSection{
		DeviceID:    id,
		Window:      w,
		Windows:     trafficWindows,
		Services:    serviceChoices(traffic),
		Recorded:    recorded,
		Traffic:     traffic,
		Summary:     summarise(traffic, attempts),
		HasAttempts: len(attempts) > 0,
		Probing:     probing,

		HasBroadcasts: len(broadcasts) > 0,
		Probed:        probed,
	}

	var tried []*inventory.Attempt
	// Tries are always the own tries of the device. So they do not go among
	// what others opened.
	if f.Tried && f.Direction != dirIn {
		tried = filterAttempts(attempts, f)
	}

	local := filterPeers(traffic.Local, f, "")
	sec.Tabs = segmentTabs(nets, local, traffic.Internet, tried, f)

	if sec.HasBroadcasts {
		sec.Tabs = append(sec.Tabs, broadcastTab(nets, broadcasts, f))
	}

	sec.Tab = pickTab(sec.Tabs, f.Tab)

	// Drop a tab that no longer exists from the address.
	if sec.Tab.Key != f.Tab {
		f.Tab = ""
	}

	sec.Filter = f

	return sec, nil
}

// devicePath is the page of a device, with the traffic period and filter when
// they are not the defaults. So a person can reload or share the address.
func devicePath(id int64, w trafficWindow, f trafficFilter) string {
	return withQuery("/devices/"+strconv.FormatInt(id, 10), trafficQuery(w, f))
}

// trafficWindowKey is the period that a request asked for. The form and the
// page name it traffic. It also accepts window, for links from before the
// section had a form.
func trafficWindowKey(q url.Values) string {
	return cmp.Or(q.Get("traffic"), q.Get("window"))
}

// deviceTraffic serves only the "Talks to" section, for its filter form.
func (h *Handler) deviceTraffic() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		// A missing device is also a 404 here.
		if err := h.store.RequireDevice(r.Context(), id); err != nil {
			return err
		}

		q := r.URL.Query()

		data, err := buildTrafficSection(r.Context(), h.store, id, trafficWindowKey(q), trafficFilterFrom(q), h.store.Now())
		if err != nil {
			return fmt.Errorf("device %d traffic: %w", id, err)
		}

		w.Header().Set("HX-Push-Url", devicePath(id, data.Window, data.Filter))

		h.htmlWriter.Success(w, r, templatePartialDeviceTraffic, data)

		return nil
	}
}

// Traffic page sizes. A card is a short view.
const (
	trafficCardRows = 10

	// firstContactRows is how many device-and-organisation pairs the page
	// reads for "New this week", before it groups them by organisation.
	firstContactRows = 200

	// trafficAllRows reads every device or organisation, for a tab that
	// limits them in Go before it takes the top of the list.
	trafficAllRows = 100_000

	// firstContactSpan is how far back "first contact" looks, for any period
	// switch. It answers "anything new this week?". A month of first contacts
	// is mostly the month when collection started.
	firstContactSpan = 7 * 24 * time.Hour
)

// trafficPage is the network-wide traffic view.
type trafficPage struct {
	view

	Window  trafficWindow
	Windows []trafficWindow

	// Group limits every card to the devices of one group. Groups are the
	// choices.
	Group  string
	Groups []string

	// Recorded tells whether Jocasta recorded any traffic.
	Recorded bool

	// Tabs are the whole network and each recorded network. Tab is the shown
	// tab. Every card below counts only the devices on it.
	Tabs []*pageTab
	Tab  *pageTab

	// Summary is a short summary of the tab.
	Summary pageSummary

	// Probers are the devices that probed the local network in the window.
	Probers []*inventory.Prober

	// Incoming are the device services to which the internet opened
	// connections.
	Incoming []*inventory.Incoming

	// Probed are the devices that the internet tried without success.
	Probed []*inventory.Probed

	// BehindNAT is set when the outside address of the router is itself
	// private. A different NAT is between it and the internet. Nothing on the
	// internet can open a connection in over IPv4. It is nil when the outside
	// address is public, or not known yet.
	BehindNAT *behindNAT

	Busiest []*inventory.DeviceTotal
	TopOrgs []*orgDevices
	First   *inventory.FirstContacts

	// NewOrgs is First, grouped by organisation, newest first. One
	// organisation that many devices started to reach is one row.
	NewOrgs []*newOrg

	// MoreNewOrgs counts the organisations after the rows of the card.
	MoreNewOrgs int

	// Attribution gives the credit for the organisation names, as their
	// licence requires.
	Attribution string
}

// pageTab is one tab of the Traffic page: the whole network, or the devices
// with an address on one recorded network.
type pageTab struct {
	Key   string
	Label string
	VLAN  int

	// Devices counts the devices on it that moved data in the window.
	Devices int

	members map[int64]bool
}

// has reports whether device id is on the tab.
func (t *pageTab) has(id int64) bool { return t.members == nil || t.members[id] }

// pageSummary is a short summary of one tab of the Traffic page.
type pageSummary struct {
	Sent, Received int64
	Devices        int
	Probing        int
}

// TabPath is the page on the tab key, with the period and group.
func (p *trafficPage) TabPath(key string) string {
	q := url.Values{}
	if p.Window.Key != trafficWindows[0].Key {
		q.Set("window", p.Window.Key)
	}

	if p.Group != "" {
		q.Set("group", p.Group)
	}

	if key != "" {
		q.Set("tab", key)
	}

	return withQuery("/traffic", q)
}

// pageTabs builds the whole-network tab and one tab for each recorded network.
// It uses the devices on each, and the devices that moved data.
func pageTabs(nets []*inventory.Network, devices []*inventory.Device, active []*inventory.DeviceTotal) []*pageTab {
	tabs := []*pageTab{{Label: "Whole network", Devices: len(active)}}

	for _, n := range nets {
		t := &pageTab{Key: networkTabKey(n.ID), Label: cmp.Or(n.Name, n.CIDR), VLAN: n.VLAN, members: map[int64]bool{}}

		for _, d := range devices {
			if slices.ContainsFunc(d.Networks, func(dn *inventory.Network) bool { return dn.ID == n.ID }) {
				t.members[d.ID] = true
			}
		}

		for _, d := range active {
			if t.members[d.DeviceID] {
				t.Devices++
			}
		}

		tabs = append(tabs, t)
	}

	return tabs
}

// orgsOnTab adds up what the devices on tab exchanged with each organisation,
// busiest first, at most limit of them. names names the organisations. One
// company that announces from several ASNs is one row, with its devices
// merged.
func orgsOnTab(
	tab *pageTab, names []*inventory.OrgTotal, byOrg map[uint32][]*inventory.DeviceTotal, limit int,
) []*orgDevices {
	var out []*orgDevices

	byShort := make(map[string]*orgDevices)

	for _, n := range names {
		o, ok := byShort[n.Short]
		if !ok {
			o = &orgDevices{OrgTotal: &inventory.OrgTotal{ASN: n.ASN, Name: n.Name, Short: n.Short}}
			byShort[n.Short] = o
			out = append(out, o)
		}

		for _, d := range byOrg[n.ASN] {
			if !tab.has(d.DeviceID) {
				continue
			}

			o.Sent += d.Sent
			o.Received += d.Received
			o.DeviceList = addDeviceTotal(o.DeviceList, d)
		}

		o.Devices = int64(len(o.DeviceList))
	}

	out = slices.DeleteFunc(out, func(o *orgDevices) bool { return o.Devices == 0 })

	for _, o := range out {
		slices.SortStableFunc(o.DeviceList, func(a, b *inventory.DeviceTotal) int {
			return cmp.Compare(b.Sent+b.Received, a.Sent+a.Received)
		})
	}

	slices.SortStableFunc(out, func(a, b *orgDevices) int {
		return cmp.Compare(b.Sent+b.Received, a.Sent+a.Received)
	})

	return out[:min(limit, len(out))]
}

// addDeviceTotal adds d to list. If the device already has an entry, it merges
// d into it.
func addDeviceTotal(list []*inventory.DeviceTotal, d *inventory.DeviceTotal) []*inventory.DeviceTotal {
	for _, e := range list {
		if e.DeviceID == d.DeviceID {
			e.Sent += d.Sent
			e.Received += d.Received

			return list
		}
	}

	c := *d

	return append(list, &c)
}

// outsideAddrWindow is how recently a router must name an outside address, for
// the page to use it.
const outsideAddrWindow = 24 * time.Hour

// behindNAT describes the NAT in front of the router: the outside address of
// the router, and whether it is in the carrier-grade range of an ISP.
type behindNAT struct {
	Addr  netip.Addr
	CGNAT bool
}

var cgnatRange = netip.MustParsePrefix("100.64.0.0/10")

// behindNATFrom tells whether the router is behind a different NAT, from its
// IPv4 outside addresses. When every one is private or carrier-grade, nothing
// on the internet reaches it. IPv6 is never translated, so it does not
// count.
func behindNATFrom(outside []netip.Addr) *behindNAT {
	var note *behindNAT

	for _, a := range outside {
		if !a.Is4() {
			continue
		}

		cgnat := cgnatRange.Contains(a)
		if !cgnat && !a.IsPrivate() {
			return nil
		}

		if note == nil {
			note = &behindNAT{Addr: a, CGNAT: cgnat}
		}
	}

	return note
}

// orgDevices is one of the busiest organisations, with the devices that reached
// it.
type orgDevices struct {
	*inventory.OrgTotal

	DeviceList []*inventory.DeviceTotal
}

// orgDeviceRows is how many devices the row of an organisation opens to. Most
// of the network can reach a big provider. That would make one open row longer
// than the card next to it.
const orgDeviceRows = 10

// DevicesHead is the devices that the row lists, the busiest first.
func (o *orgDevices) DevicesHead() []*inventory.DeviceTotal {
	return o.DeviceList[:min(orgDeviceRows, len(o.DeviceList))]
}

// DevicesRest counts the devices that DevicesHead leaves out.
func (o *orgDevices) DevicesRest() int64 { return int64(max(0, len(o.DeviceList)-orgDeviceRows)) }

// newOrg is an organisation that some devices reached for the first time.
type newOrg struct {
	ASN         uint32
	Name, Short string

	// First is the earliest of the first contacts of its devices.
	First   time.Time
	Bytes   int64
	Devices []*inventory.FirstContact
}

// groupFirstContacts merges first contacts by organisation. It keeps the order
// of the newest contact of each organisation.
func groupFirstContacts(contacts []*inventory.FirstContact) []*newOrg {
	byShort := make(map[string]*newOrg)

	var out []*newOrg

	for _, c := range contacts {
		o, ok := byShort[c.Short]
		if !ok {
			o = &newOrg{ASN: c.ASN, Name: c.Name, Short: c.Short, First: c.First}
			byShort[c.Short] = o
			out = append(out, o)
		}

		o.Devices = addFirstContact(o.Devices, c)
		o.Bytes += c.Bytes

		if c.First.Before(o.First) {
			o.First = c.First
		}
	}

	return out
}

// addFirstContact adds c to list. It merges c into the entry of the device when
// the device reached the same organisation through a different ASN.
func addFirstContact(list []*inventory.FirstContact, c *inventory.FirstContact) []*inventory.FirstContact {
	for _, e := range list {
		if e.DeviceID == c.DeviceID {
			e.Bytes += c.Bytes

			if c.First.Before(e.First) {
				e.First = c.First
			}

			return list
		}
	}

	cp := *c

	return append(list, &cp)
}

// traffic serves the network-wide traffic page.
func (h *Handler) traffic(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		now := h.store.Now()
		q := r.URL.Query()
		win := windowFor(q.Get("window"))

		data := &trafficPage{
			view: view{
				Title: "Traffic", Section: "Traffic",
				Role: sm.CurrentRole(ctx), SignedInAs: sm.CurrentUsername(ctx),
			},
			Window:      win,
			Windows:     trafficWindows,
			Attribution: asn.Attribution,
		}

		var err error

		if data.Groups, err = h.store.Groups(ctx); err != nil {
			return err
		}

		// Drop a group with no devices. An empty page would not tell why.
		if g := strings.TrimSpace(q.Get("group")); slices.Contains(data.Groups, g) {
			data.Group = g
		}

		if data.Recorded, err = h.store.TrafficRecorded(ctx); err != nil {
			return err
		}

		// With nothing recorded, the page is only the setup note. So the page
		// would show none of the reads of the cards.
		if !data.Recorded {
			data.Note = latestSweepNote(ctx, h.store)
			h.htmlWriter.Success(w, r, templatePageTraffic, data)

			return nil
		}

		since := now.Add(-win.span)

		nets, err := h.store.ListNetworks(ctx)
		if err != nil {
			return err
		}

		devices, err := h.store.ListDevices(ctx, inventory.DeviceFilter{Group: data.Group})
		if err != nil {
			return err
		}

		active, err := h.store.BusiestDevices(ctx, since, data.Group, trafficAllRows)
		if err != nil {
			return err
		}

		data.Tabs = pageTabs(nets, devices, active)
		data.Tab = data.Tabs[0]

		for _, t := range data.Tabs {
			if t.Key != "" && t.Key == q.Get("tab") {
				data.Tab = t
			}
		}

		for _, d := range active {
			if !data.Tab.has(d.DeviceID) {
				continue
			}

			data.Summary.Sent += d.Sent
			data.Summary.Received += d.Received
			data.Summary.Devices++

			if len(data.Busiest) < trafficCardRows {
				data.Busiest = append(data.Busiest, d)
			}
		}

		probers, err := h.store.ProbingDevices(ctx, since, data.Group)
		if err != nil {
			return err
		}

		for _, p := range probers {
			if data.Tab.has(p.DeviceID) {
				data.Probers = append(data.Probers, p)
			}
		}

		data.Summary.Probing = len(data.Probers)

		incoming, err := h.store.IncomingFromInternet(ctx, since, data.Group, trafficAllRows)
		if err != nil {
			return err
		}

		for _, in := range incoming {
			if data.Tab.has(in.DeviceID) && len(data.Incoming) < trafficCardRows {
				data.Incoming = append(data.Incoming, in)
			}
		}

		outside, err := h.store.OutsideAddresses(ctx, now.Add(-outsideAddrWindow))
		if err != nil {
			return err
		}

		data.BehindNAT = behindNATFrom(outside)

		probed, err := h.store.ProbedDevices(ctx, since, data.Group)
		if err != nil {
			return err
		}

		for _, p := range probed {
			if data.Tab.has(p.DeviceID) && len(data.Probed) < trafficCardRows {
				data.Probed = append(data.Probed, p)
			}
		}

		orgs, err := h.store.TopOrganisations(ctx, since, data.Group, trafficAllRows)
		if err != nil {
			return err
		}

		byOrg, err := h.store.OrganisationDevices(ctx, since, data.Group)
		if err != nil {
			return err
		}

		data.TopOrgs = orgsOnTab(data.Tab, orgs, byOrg, trafficCardRows)

		if data.First, err = h.store.FirstContacts(ctx, now.Add(-firstContactSpan), data.Group, firstContactRows); err != nil {
			return err
		}

		var contacts []*inventory.FirstContact

		for _, c := range data.First.Contacts {
			if data.Tab.has(c.DeviceID) {
				contacts = append(contacts, c)
			}
		}

		data.NewOrgs = groupFirstContacts(contacts)

		// Until a week is recorded, every organisation is new. Newest first is
		// only the order in which collection found them. The organisations
		// that moved the most are worth a look.
		if data.First.Partial {
			slices.SortStableFunc(data.NewOrgs, func(a, b *newOrg) int { return cmp.Compare(b.Bytes, a.Bytes) })
		}

		data.MoreNewOrgs = max(0, len(data.NewOrgs)-trafficCardRows)
		data.NewOrgs = data.NewOrgs[:min(trafficCardRows, len(data.NewOrgs))]

		data.Note = latestSweepNote(ctx, h.store)

		h.htmlWriter.Success(w, r, templatePageTraffic, data)

		return nil
	}
}
