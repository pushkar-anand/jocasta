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

	// Local and Internet are Traffic after the filter, grouped for reading.
	Local    []*localEntry
	Internet []*orgEntry

	// Attempts are the connections the device tried that never carried
	// data, after the filter; HasAttempts is whether there were any before
	// it. Probing is set when they add up to probing the network.
	Attempts    []*attemptEntry
	HasAttempts bool
	Probing     *inventory.Prober
}

// Empty reports whether the device neither talked nor tried anything in the
// window.
func (t *trafficSection) Empty() bool {
	return t.Traffic.Empty() && !t.HasAttempts
}

// Matched reports whether anything is left once the filter is applied.
func (t *trafficSection) Matched() bool {
	return len(t.Local) > 0 || len(t.Internet) > 0 || len(t.Attempts) > 0
}

// ClearPath is the device page with the period kept and every filter dropped.
func (t *trafficSection) ClearPath() string {
	return devicePath(t.DeviceID, t.Window, trafficFilter{})
}

// buildTrafficSection reads one device's traffic over the window key names,
// narrowed by f.
func buildTrafficSection(
	ctx context.Context, store *inventory.Store, id int64, key string, f trafficFilter, now time.Time,
) (*trafficSection, error) {
	w := windowFor(key)

	recorded, err := store.TrafficRecorded(ctx)
	if err != nil {
		return nil, err
	}

	traffic, err := store.DeviceTraffic(ctx, id, now.Add(-w.span))
	if err != nil {
		return nil, err
	}

	sec := &trafficSection{
		DeviceID: id,
		Window:   w,
		Windows:  trafficWindows,
		Filter:   f,
		Services: serviceChoices(traffic),
		Recorded: recorded,
		Traffic:  traffic,
	}

	sec.Local, sec.Internet = groupTraffic(traffic, f)

	attempts, err := store.DeviceAttempts(ctx, id, now.Add(-w.span))
	if err != nil {
		return nil, err
	}

	sec.HasAttempts = len(attempts) > 0
	sec.Attempts = groupAttempts(attempts, f)

	probers, err := store.ProbingDevices(ctx, now.Add(-w.span), "")
	if err != nil {
		return nil, err
	}

	sec.Probing = proberFor(probers, id)

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

	// Probers are the devices that probed the local network in the window.
	Probers []*inventory.Prober

	Busiest []*inventory.DeviceTotal
	TopOrgs []*orgDevices
	First   *inventory.FirstContacts

	// NewOrgs is First grouped by organisation, newest first: one
	// organisation many devices started reaching is one row, not one each.
	NewOrgs []*newOrg

	// Attribution credits the organisation names, as their licence requires.
	Attribution string
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
	byASN := make(map[uint32]*newOrg)

	var out []*newOrg

	for _, c := range contacts {
		o, ok := byASN[c.ASN]
		if !ok {
			o = &newOrg{ASN: c.ASN, Name: c.Name, Short: c.Short, First: c.First}
			byASN[c.ASN] = o
			out = append(out, o)
		}

		o.Devices = append(o.Devices, c)
		o.Bytes += c.Bytes

		if c.First.Before(o.First) {
			o.First = c.First
		}
	}

	return out
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

		if data.Probers, err = h.store.ProbingDevices(ctx, since, data.Group); err != nil {
			return err
		}

		if data.Busiest, err = h.store.BusiestDevices(ctx, since, data.Group, trafficCardRows); err != nil {
			return err
		}

		orgs, err := h.store.TopOrganisations(ctx, since, data.Group, trafficCardRows)
		if err != nil {
			return err
		}

		byOrg, err := h.store.OrganisationDevices(ctx, since, data.Group)
		if err != nil {
			return err
		}

		for _, o := range orgs {
			data.TopOrgs = append(data.TopOrgs, &orgDevices{OrgTotal: o, DeviceList: byOrg[o.ASN]})
		}

		if data.First, err = h.store.FirstContacts(ctx, now.Add(-firstContactSpan), data.Group, firstContactRows); err != nil {
			return err
		}

		data.NewOrgs = groupFirstContacts(data.First.Contacts)

		if note, err := h.sweepNote(ctx); err == nil {
			data.Note = note
		}

		h.htmlWriter.Success(w, r, templatePageTraffic, data)

		return nil
	}
}
