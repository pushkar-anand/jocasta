package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
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

// trafficWindows are the periods offered, shortest first. The first is the
// default: "today, roughly" is the question most visits ask.
var trafficWindows = []trafficWindow{
	{Key: "24h", Label: "24 hours", span: 24 * time.Hour},
	{Key: "7d", Label: "7 days", span: 7 * 24 * time.Hour},
	{Key: "30d", Label: "30 days", span: 30 * 24 * time.Hour},
}

// windowFor returns the window key names, falling back to the default for
// anything else rather than refusing: it only ever arrives from a link.
func windowFor(key string) trafficWindow {
	for _, w := range trafficWindows {
		if w.Key == key {
			return w
		}
	}

	return trafficWindows[0]
}

// trafficSection is a device's "Talks to" section, on the page and as the
// fragment its filter form fetches.
type trafficSection struct {
	DeviceID int64
	Window   trafficWindow
	Windows  []trafficWindow
	Filter   trafficFilter

	// Services are the services the device used over the window, for the
	// filter's choices. Taken before filtering, so picking one never empties
	// the list it was picked from.
	Services []serviceChoice

	// Recorded is whether any traffic has been recorded at all, which is how
	// the section tells "this device exchanged nothing" from "nothing is
	// collecting".
	Recorded bool
	Traffic  *inventory.DeviceTraffic

	// Summary is the window at a glance, before the filter.
	Summary trafficSummary

	// Tabs split what is left after the filter by network segment; Tab is the
	// one shown.
	Tabs []*trafficTab
	Tab  *trafficTab

	// HasAttempts is whether the device tried anything that never carried
	// data, whether or not those tries are shown. Probing is set when they add
	// up to probing the network.
	HasAttempts bool
	Probing     *inventory.Prober
}

// Empty reports whether the device neither talked nor tried anything in the
// window.
func (t *trafficSection) Empty() bool {
	return t.Traffic.Empty() && !t.HasAttempts
}

// query is the section's address with f in place of its filter.
func (t *trafficSection) query(f trafficFilter) url.Values {
	q := url.Values{}
	if t.Window.Key != trafficWindows[0].Key {
		q.Set("traffic", t.Window.Key)
	}

	f.encode(q)

	return q
}

// TabPath is the device page on the tab key, filter kept.
func (t *trafficSection) TabPath(key string) string {
	f := t.Filter
	f.Tab = key

	return devicePath(t.DeviceID, t.Window, f)
}

// TabFragment is the section alone on the tab key, for htmx.
func (t *trafficSection) TabFragment(key string) string {
	f := t.Filter
	f.Tab = key

	return fragmentPath(t.DeviceID, t.query(f))
}

// TriedPath and TriedFragment flip whether tries are shown, on the same tab.
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

// ClearPath is the device page with the period, tab and tries switch kept and
// the search and service dropped.
func (t *trafficSection) ClearPath() string {
	return devicePath(t.DeviceID, t.Window, trafficFilter{Tab: t.Tab.Key, Tried: t.Filter.Tried})
}

// ClearFragment is ClearPath for htmx.
func (t *trafficSection) ClearFragment() string {
	return fragmentPath(t.DeviceID, t.query(trafficFilter{Tab: t.Tab.Key, Tried: t.Filter.Tried}))
}

func fragmentPath(id int64, q url.Values) string {
	p := "/devices/" + strconv.FormatInt(id, 10) + "/traffic"
	if len(q) > 0 {
		p += "?" + q.Encode()
	}

	return p
}

// buildTrafficSection reads one device's traffic over the window key names,
// narrowed by f.
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

	probers, err := store.ProbingDevices(ctx, since, "")
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
		Probing:     proberFor(probers, id),
	}

	var tried []*inventory.Attempt
	if f.Tried {
		tried = filterAttempts(attempts, f)
	}

	local := filterPeers(traffic.Local, f.Service, strings.ToLower(f.Query), "")
	sec.Tabs = segmentTabs(nets, local, traffic.Internet, tried, f)
	sec.Tab = pickTab(sec.Tabs, f.Tab)

	// A tab that is not there any more is dropped from the address rather
	// than kept pointing at nothing.
	if sec.Tab.Key != f.Tab {
		f.Tab = ""
	}

	sec.Filter = f

	return sec, nil
}

// devicePath is a device's page with the traffic period and filter when they
// are not the defaults, so the address bar can be reloaded or shared.
func devicePath(id int64, w trafficWindow, f trafficFilter) string {
	q := url.Values{}
	if w.Key != trafficWindows[0].Key {
		q.Set("traffic", w.Key)
	}

	f.encode(q)

	p := "/devices/" + strconv.FormatInt(id, 10)
	if len(q) > 0 {
		p += "?" + q.Encode()
	}

	return p
}

// trafficWindowKey is the period a request asked for. The form and the page
// call it traffic; window is what the section's links sent before it had a
// form, and still works.
func trafficWindowKey(q url.Values) string {
	return cmp.Or(q.Get("traffic"), q.Get("window"))
}

// deviceTraffic serves the "Talks to" section alone, for its filter form.
func (h *Handler) deviceTraffic() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		// A device that is not there is a 404 here too, not an empty section.
		if _, err := h.store.Device(r.Context(), id); err != nil {
			return err
		}

		q := r.URL.Query()

		data, err := buildTrafficSection(r.Context(), h.store, id, trafficWindowKey(q), trafficFilterFrom(q), time.Now())
		if err != nil {
			return fmt.Errorf("device %d traffic: %w", id, err)
		}

		w.Header().Set("HX-Push-Url", devicePath(id, data.Window, data.Filter))

		h.htmlWriter.Success(w, r, templatePartialDeviceTraffic, data)

		return nil
	}
}

// Traffic page sizes: a card is a glance, not a report.
const (
	trafficCardRows = 10

	// firstContactRows is how many device-and-organisation pairs are read for
	// "New this week", before they are grouped by organisation.
	firstContactRows = 200

	// trafficAllRows reads every device or organisation, for a tab that
	// narrows them in Go before taking the top of the list.
	trafficAllRows = 100_000

	// firstContactSpan is how far back "first contact" looks. Fixed rather
	// than following the period switch: it answers "anything new this week?",
	// and a month of first contacts is mostly the month collection started.
	firstContactSpan = 7 * 24 * time.Hour
)

// trafficPage is the network-wide traffic view.
type trafficPage struct {
	view

	Window  trafficWindow
	Windows []trafficWindow

	// Group narrows every card to one group's devices; Groups are the
	// choices.
	Group  string
	Groups []string

	// Recorded is whether any traffic has been recorded at all.
	Recorded bool

	// Tabs are the whole network and each recorded network; Tab is the one
	// shown, and every card below counts only the devices on it.
	Tabs []*pageTab
	Tab  *pageTab

	// Summary is the tab at a glance.
	Summary pageSummary

	// Probers are the devices that probed the local network in the window.
	Probers []*inventory.Prober

	Busiest []*inventory.DeviceTotal
	TopOrgs []*orgDevices
	First   *inventory.FirstContacts

	// NewOrgs is First grouped by organisation, newest first: one
	// organisation many devices started reaching is one row, not one each.
	NewOrgs []*newOrg

	// MoreNewOrgs counts the organisations past the card's rows.
	MoreNewOrgs int

	// Attribution credits the organisation names, as their licence requires.
	Attribution string
}

// pageTab is one tab of the Traffic page: the whole network, or the devices
// holding an address on one recorded network.
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

// pageSummary is one Traffic page tab at a glance.
type pageSummary struct {
	Sent, Received int64
	Devices        int
	Probing        int
}

// TabPath is the page on the tab key, period and group kept.
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

	if len(q) == 0 {
		return "/traffic"
	}

	return "/traffic?" + q.Encode()
}

// pageTabs builds the whole-network tab and one per recorded network, from
// which devices are on each and which moved data.
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
// busiest first, at most limit of them. names names the organisations; one
// company announcing from several ASNs is one row, its devices merged.
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

// addDeviceTotal adds d to list, folding it into the device's entry when it
// is already there.
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

// orgDevices is one of the busiest organisations and which devices reached it.
type orgDevices struct {
	*inventory.OrgTotal

	DeviceList []*inventory.DeviceTotal
}

// newOrg is an organisation some devices reached for the first time.
type newOrg struct {
	ASN         uint32
	Name, Short string

	// First is the earliest of its devices' first contacts.
	First   time.Time
	Bytes   int64
	Devices []*inventory.FirstContact
}

// groupFirstContacts folds first contacts by organisation, keeping the order
// of each organisation's newest contact.
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

// addFirstContact adds c to list, folding it into the device's entry when the
// device reached the same organisation through another of its ASNs.
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
		now := time.Now()
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

		// A group nobody is in any more is dropped rather than showing an
		// empty page with no way to tell why.
		if g := strings.TrimSpace(q.Get("group")); slices.Contains(data.Groups, g) {
			data.Group = g
		}

		if data.Recorded, err = h.store.TrafficRecorded(ctx); err != nil {
			return err
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

		// Until a week has been recorded every organisation is new, and
		// newest first is only the order collection happened to meet them;
		// the ones that moved the most are the ones worth a look.
		if data.First.Partial {
			slices.SortStableFunc(data.NewOrgs, func(a, b *newOrg) int { return cmp.Compare(b.Bytes, a.Bytes) })
		}

		data.MoreNewOrgs = max(0, len(data.NewOrgs)-trafficCardRows)
		data.NewOrgs = data.NewOrgs[:min(trafficCardRows, len(data.NewOrgs))]

		if note, err := h.sweepNote(ctx); err == nil {
			data.Note = note
		}

		h.htmlWriter.Success(w, r, templatePageTraffic, data)

		return nil
	}
}
