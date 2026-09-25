package web

import (
	"cmp"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// trafficFilter narrows a device's traffic. The zero value shows the busiest
// tab, conversations only, unfiltered.
type trafficFilter struct {
	// Tab is the key of the segment tab shown, empty for the busiest.
	Tab string

	// Service is a serviceChoice key: protocol and port, "6/443".
	Service string

	// Query matches, ignoring case, anything the section names a peer by:
	// device name, address, DNS name, organisation, service.
	Query string

	// Tried adds the connections that never carried data to the rows.
	Tried bool

	// Direction keeps only the peers connections went to one way: dirOut
	// those this device opened connections to, dirIn those that opened
	// connections to it. Empty keeps both.
	Direction string
}

// Directions the traffic can be narrowed to.
const (
	dirOut = "out"
	dirIn  = "in"
)

// trafficFilterFrom reads a filter from a query string. A value it does not
// know is dropped: values only ever arrive from the form.
// An unknown tab is dropped once the tabs are known.
func trafficFilterFrom(q url.Values) trafficFilter {
	f := trafficFilter{
		Tab:     strings.TrimSpace(q.Get("tab")),
		Service: strings.TrimSpace(q.Get("service")),
		Query:   strings.TrimSpace(q.Get("q")),
		Tried:   q.Get("tried") == "1",
	}

	if d := q.Get("dir"); d == dirOut || d == dirIn {
		f.Direction = d
	}

	if _, _, ok := parseServiceKey(f.Service); !ok {
		f.Service = ""
	}

	return f
}

// Active reports whether the filter narrows the rows. The tab and the tries
// switch only choose what is shown, so clearing the filter keeps them.
func (f trafficFilter) Active() bool {
	return f.Service != "" || f.Query != "" || f.Direction != ""
}

func (f trafficFilter) encode(q url.Values) {
	for k, v := range map[string]string{"tab": f.Tab, "service": f.Service, "q": f.Query, "dir": f.Direction} {
		if v != "" {
			q.Set(k, v)
		}
	}

	if f.Tried {
		q.Set("tried", "1")
	}
}

// serviceChoice is one entry in the service filter.
type serviceChoice struct {
	Key   string
	Label string
}

func serviceKey(p *inventory.TrafficPeer) string {
	return strconv.Itoa(int(p.Protocol)) + "/" + strconv.Itoa(int(p.ServicePort))
}

func parseServiceKey(k string) (protocol uint8, port uint16, ok bool) {
	a, b, found := strings.Cut(k, "/")
	if !found {
		return 0, 0, false
	}

	pr, err := strconv.ParseUint(a, 10, 8)
	if err != nil {
		return 0, 0, false
	}

	po, err := strconv.ParseUint(b, 10, 16)
	if err != nil {
		return 0, 0, false
	}

	return uint8(pr), uint16(po), true
}

// serviceLabel is a peer's service as a person reads it.
func serviceLabel(p *inventory.TrafficPeer) string {
	return serviceName(p.Protocol, p.ServicePort, p.Service)
}

// serviceName is a service as a person reads it: its usual name, or its port
// when it has none, with the protocol when that is not TCP. Port zero is the
// protocol alone.
func serviceName(protocol uint8, port uint16, name string) string {
	if port == 0 {
		return cmp.Or(protoName(protocol), "TCP")
	}

	label := cmp.Or(name, "port "+strconv.Itoa(int(port)))
	if proto := protoName(protocol); proto != "" {
		label += " " + proto
	}

	return label
}

// serviceChoices lists every service in t once, by name.
func serviceChoices(t *inventory.DeviceTraffic) []serviceChoice {
	seen := make(map[string]bool)

	var out []serviceChoice

	for _, p := range allPeers(t) {
		if k := serviceKey(p); !seen[k] {
			seen[k] = true
			out = append(out, serviceChoice{Key: k, Label: serviceLabel(p)})
		}
	}

	slices.SortFunc(out, func(a, b serviceChoice) int { return cmp.Compare(a.Label, b.Label) })

	return out
}

func allPeers(t *inventory.DeviceTraffic) []*inventory.TrafficPeer {
	out := slices.Clone(t.Local)
	for _, o := range t.Internet {
		out = append(out, o.Peers...)
	}

	return out
}

// peerRow is everything a device exchanged with one peer, across services,
// and what it tried there that never carried data.
type peerRow struct {
	DeviceID   int64
	DeviceName string
	IP         netip.Addr
	Name       string

	// Services are the per-service rows behind this one, busiest first.
	Services []*inventory.TrafficPeer

	// Tried are the connections to the peer that carried nothing, when the
	// section shows them.
	Tried []*inventory.Attempt

	Sent, Received  int64
	Tries, Answered int64
	LastHour        time.Time

	// Outgoing counts the connections the device opened on the peer,
	// Incoming the ones the peer opened on the device.
	Outgoing, Incoming int64
}

// ServiceSummary names the peer's two busiest services and how many more.
func (r *peerRow) ServiceSummary() string {
	var names []string

	for _, s := range r.Services[:min(2, len(r.Services))] {
		names = append(names, serviceLabel(s))
	}

	out := strings.Join(names, ", ")
	if more := len(r.Services) - len(names); more > 0 {
		out += fmt.Sprintf(" +%d more", more)
	}

	return out
}

// TriedSummary says what was tried at the peer: pings, or which ports.
func (r *peerRow) TriedSummary() string {
	var names []string

	for _, a := range r.Tried[:min(2, len(r.Tried))] {
		names = append(names, attemptWhat(a))
	}

	out := strings.Join(names, "; ")
	if more := len(r.Tried) - len(names); more > 0 {
		out += fmt.Sprintf(" +%d more", more)
	}

	return out
}

// ServiceLabel is one of the row's services as a person reads it, for the
// template's breakdown.
func (r *peerRow) ServiceLabel(p *inventory.TrafficPeer) string { return serviceLabel(p) }

func (r *peerRow) total() int64 { return r.Sent + r.Received }

func (r *peerRow) add(p *inventory.TrafficPeer) {
	r.Services = append(r.Services, p)
	r.Sent += p.Sent
	r.Received += p.Received
	r.Outgoing += p.Connections
	r.Incoming += p.ConnectionsIn
	r.seen(p.LastHour)
}

func (r *peerRow) try(a *inventory.Attempt) {
	r.Tried = append(r.Tried, a)
	r.Tries += a.Attempts
	r.Answered += a.Answered
	r.seen(a.LastHour)
}

func (r *peerRow) seen(hour time.Time) {
	if hour.After(r.LastHour) {
		r.LastHour = hour
	}
}

// busier orders rows by what they moved, then by how often they were tried.
func busier(aBytes, aTries, bBytes, bTries int64) int {
	return cmp.Or(cmp.Compare(bBytes, aBytes), cmp.Compare(bTries, aTries))
}

// peerTally is what a set of rows adds up to.
type peerTally struct {
	Sent, Received  int64
	Tries, Answered int64
	Outgoing        int64
	Incoming        int64
	LastHour        time.Time
}

func (t *peerTally) add(r *peerRow) {
	t.Sent += r.Sent
	t.Received += r.Received
	t.Outgoing += r.Outgoing
	t.Incoming += r.Incoming
	t.Tries += r.Tries
	t.Answered += r.Answered

	if r.LastHour.After(t.LastHour) {
		t.LastHour = r.LastHour
	}
}

// peerListHead is how many addresses a folded row opens to before the rest
// are only counted: a sweep of a subnet is hundreds of near-identical lines.
const peerListHead = 20

// peerList is the peers a folded row opens to.
type peerList []*peerRow

// Head is the busiest of them, as many as a person reads.
func (l peerList) Head() peerList { return l[:min(peerListHead, len(l))] }

// Rest counts the ones Head leaves out.
func (l peerList) Rest() int64 { return int64(max(0, len(l)-peerListHead)) }

// strangerGroup is local addresses no device holds, collapsed by the subnet
// they sit in: a device reaching many unknown hosts next to each other is one
// fact, shown as one row.
type strangerGroup struct {
	peerTally

	Prefix netip.Prefix
	Peers  peerList
}

// localEntry is one row of a segment tab: a peer on its own, or a group.
type localEntry struct {
	Peer  *peerRow
	Group *strangerGroup
}

func (e *localEntry) order() (bytes, tries int64) {
	if e.Group != nil {
		return e.Group.Sent + e.Group.Received, e.Group.Tries
	}

	return e.Peer.total(), e.Peer.Tries
}

// orgEntry is one organisation on the Internet tab, with its addresses merged
// across services.
type orgEntry struct {
	peerTally

	ASN         uint32
	Name, Short string
	Peers       peerList
}

// filterPeers keeps the peers on f's service, connected the way f's direction
// asks, whose names contain its query. org is the lowercased organisation the
// peers belong to, which matches all of them.
func filterPeers(peers []*inventory.TrafficPeer, f trafficFilter, org string) []*inventory.TrafficPeer {
	var out []*inventory.TrafficPeer

	query := strings.ToLower(f.Query)

	for _, p := range peers {
		switch {
		case f.Service != "" && serviceKey(p) != f.Service,
			f.Direction == dirOut && p.Connections == 0,
			f.Direction == dirIn && p.ConnectionsIn == 0:
			continue
		}

		if query != "" && !strings.Contains(org, query) && !peerMatches(p, query) {
			continue
		}

		out = append(out, p)
	}

	return out
}

func peerMatches(p *inventory.TrafficPeer, query string) bool {
	return anyContains(query, p.DeviceName, p.IP.String(), p.Name, serviceLabel(p))
}

// mergePeers folds per-service rows and tries into one row per peer, busiest
// first. A known device is one peer whatever address it used; anything else
// is its address.
func mergePeers(peers []*inventory.TrafficPeer, tried []*inventory.Attempt) []*peerRow {
	type key struct {
		device int64
		ip     netip.Addr
	}

	byPeer := make(map[key]*peerRow)

	var out []*peerRow

	row := func(device int64, name string, ip netip.Addr) *peerRow {
		k := key{device: device}
		if device == 0 {
			k.ip = ip
		}

		r, ok := byPeer[k]
		if !ok {
			r = &peerRow{DeviceID: device, DeviceName: name, IP: ip}
			byPeer[k] = r
			out = append(out, r)
		}

		return r
	}

	for _, p := range peers {
		r := row(p.DeviceID, p.DeviceName, p.IP)
		r.Name = cmp.Or(r.Name, p.Name)
		r.add(p)
	}

	for _, a := range tried {
		row(a.PeerDeviceID, a.PeerDeviceName, a.IP).try(a)
	}

	for _, r := range out {
		slices.SortStableFunc(r.Services, func(a, b *inventory.TrafficPeer) int {
			return cmp.Compare(b.Sent+b.Received, a.Sent+a.Received)
		})
	}

	slices.SortStableFunc(out, func(a, b *peerRow) int {
		return busier(a.total(), a.Tries, b.total(), b.Tries)
	})

	return out
}

// groupLocal turns local peers and tries into rows, collapsing two or more
// unknown addresses in one subnet into a single entry.
func groupLocal(peers []*inventory.TrafficPeer, tried []*inventory.Attempt) []*localEntry {
	var (
		out     []*localEntry
		byNet   = make(map[netip.Prefix][]*peerRow)
		netList []netip.Prefix
	)

	for _, r := range mergePeers(peers, tried) {
		if r.DeviceID != 0 {
			out = append(out, &localEntry{Peer: r})

			continue
		}

		bits := 24
		if r.IP.Is6() {
			bits = 64
		}

		pfx, err := r.IP.Prefix(bits)
		if err != nil {
			out = append(out, &localEntry{Peer: r})

			continue
		}

		if _, ok := byNet[pfx]; !ok {
			netList = append(netList, pfx)
		}

		byNet[pfx] = append(byNet[pfx], r)
	}

	for _, pfx := range netList {
		rows := byNet[pfx]
		if len(rows) == 1 {
			out = append(out, &localEntry{Peer: rows[0]})

			continue
		}

		g := &strangerGroup{Prefix: pfx, Peers: rows}
		for _, r := range rows {
			g.add(r)
		}

		out = append(out, &localEntry{Group: g})
	}

	slices.SortStableFunc(out, func(a, b *localEntry) int {
		ab, at := a.order()
		bb, bt := b.order()

		return busier(ab, at, bb, bt)
	})

	return out
}

// orgKey is what folds internet peers into one row: the organisation's short
// name, so one company announcing from several ASNs is one row, or the address
// when no organisation announces it.
func orgKey(asn uint32, short string, ip netip.Addr) string {
	if asn == 0 {
		return "ip:" + ip.String()
	}

	return "org:" + short
}

// groupInternet applies the service and query filter to the organisations a
// device exchanged data with, and adds the tries, already filtered, to the
// organisation announcing each address. An address no organisation announces
// is its own entry.
func groupInternet(orgs []*inventory.TrafficOrg, tried []*inventory.Attempt, f trafficFilter) []*orgEntry {
	type staged struct {
		entry *orgEntry
		peers []*inventory.TrafficPeer
		tried []*inventory.Attempt
	}

	var order []*staged

	byKey := make(map[string]*staged)

	stage := func(key string, e *orgEntry) *staged {
		st, ok := byKey[key]
		if !ok {
			st = &staged{entry: e}
			byKey[key] = st
			order = append(order, st)
		}

		return st
	}

	for _, o := range orgs {
		peers := filterPeers(o.Peers, f, strings.ToLower(o.Name+" "+o.Short))
		if len(peers) == 0 {
			continue
		}

		st := stage(orgKey(o.ASN, o.Short, peers[0].IP), &orgEntry{ASN: o.ASN, Name: o.Name, Short: o.Short})
		st.peers = append(st.peers, peers...)
	}

	for _, a := range tried {
		label := cmp.Or(a.OrgShort, a.IP.String())
		st := stage(orgKey(a.ASN, a.OrgShort, a.IP), &orgEntry{ASN: a.ASN, Name: label, Short: label})
		st.tried = append(st.tried, a)
	}

	out := make([]*orgEntry, 0, len(order))

	for _, st := range order {
		e := st.entry
		e.Peers = mergePeers(st.peers, st.tried)

		for _, r := range e.Peers {
			e.add(r)
		}

		out = append(out, e)
	}

	slices.SortStableFunc(out, func(a, b *orgEntry) int {
		return busier(a.Sent+a.Received, a.Tries, b.Sent+b.Received, b.Tries)
	})

	return out
}
