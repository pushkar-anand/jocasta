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

// Traffic scopes the device section can be narrowed to.
const (
	trafficScopeLocal    = "local"
	trafficScopeInternet = "internet"
)

// trafficFilter narrows a device's traffic. The zero value shows everything.
type trafficFilter struct {
	// Scope is empty for both halves, or one of the trafficScope constants.
	Scope string

	// Service is a serviceChoice key: protocol and port, "6/443".
	Service string

	// Query matches, ignoring case, anything the section names a peer by:
	// device name, address, DNS name, organisation, service.
	Query string
}

// trafficFilterFrom reads a filter from a query string. A value it does not
// know is dropped rather than refused: they only ever arrive from the form.
func trafficFilterFrom(q url.Values) trafficFilter {
	f := trafficFilter{
		Service: strings.TrimSpace(q.Get("service")),
		Query:   strings.TrimSpace(q.Get("q")),
	}

	if s := q.Get("scope"); s == trafficScopeLocal || s == trafficScopeInternet {
		f.Scope = s
	}

	if _, _, ok := parseServiceKey(f.Service); !ok {
		f.Service = ""
	}

	return f
}

// Active reports whether the filter narrows anything.
func (f trafficFilter) Active() bool {
	return f != trafficFilter{}
}

func (f trafficFilter) encode(q url.Values) {
	for k, v := range map[string]string{"scope": f.Scope, "service": f.Service, "q": f.Query} {
		if v != "" {
			q.Set(k, v)
		}
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

// serviceLabel is a service as a person reads it: its usual name, or its port
// when it has none, with the protocol when that is not TCP.
func serviceLabel(p *inventory.TrafficPeer) string {
	if p.ServicePort == 0 {
		return cmp.Or(protoName(p.Protocol), "TCP")
	}

	label := cmp.Or(p.Service, "port "+strconv.Itoa(int(p.ServicePort)))
	if proto := protoName(p.Protocol); proto != "" {
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

// peerRow is everything a device exchanged with one peer, across services.
type peerRow struct {
	DeviceID   int64
	DeviceName string
	IP         netip.Addr
	Name       string

	// Services are the per-service rows behind this one, busiest first.
	Services []*inventory.TrafficPeer

	Sent, Received int64
	LastHour       time.Time
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

// ServiceLabel is one of the row's services as a person reads it, for the
// template's breakdown.
func (r *peerRow) ServiceLabel(p *inventory.TrafficPeer) string { return serviceLabel(p) }

func (r *peerRow) add(p *inventory.TrafficPeer) {
	r.Services = append(r.Services, p)
	r.Sent += p.Sent
	r.Received += p.Received

	if p.LastHour.After(r.LastHour) {
		r.LastHour = p.LastHour
	}
}

// strangerGroup is local addresses no device holds, collapsed by the subnet
// they sit in: a device reaching many unknown hosts next to each other is one
// fact, not a screenful.
type strangerGroup struct {
	Prefix         netip.Prefix
	Peers          []*peerRow
	Sent, Received int64
	LastHour       time.Time
}

// localEntry is one row of "On your network": a peer on its own, or a group.
type localEntry struct {
	Peer  *peerRow
	Group *strangerGroup
}

func (e *localEntry) total() int64 {
	if e.Group != nil {
		return e.Group.Sent + e.Group.Received
	}

	return e.Peer.Sent + e.Peer.Received
}

// orgEntry is one organisation in "Internet", with its addresses merged across
// services.
type orgEntry struct {
	ASN            uint32
	Name, Short    string
	Peers          []*peerRow
	Sent, Received int64
	LastHour       time.Time
}

// groupTraffic applies f to t and groups what is left: local peers one row
// each, with the unknown ones collapsed by subnet, and internet peers by
// organisation.
func groupTraffic(t *inventory.DeviceTraffic, f trafficFilter) ([]*localEntry, []*orgEntry) {
	query := strings.ToLower(f.Query)

	var (
		local    []*localEntry
		internet []*orgEntry
	)

	if f.Scope != trafficScopeInternet {
		local = groupLocal(filterPeers(t.Local, f.Service, query, ""))
	}

	if f.Scope != trafficScopeLocal {
		for _, o := range t.Internet {
			peers := filterPeers(o.Peers, f.Service, query, strings.ToLower(o.Name+" "+o.Short))
			if len(peers) == 0 {
				continue
			}

			e := &orgEntry{ASN: o.ASN, Name: o.Name, Short: o.Short, Peers: mergePeers(peers)}
			for _, r := range e.Peers {
				e.Sent += r.Sent
				e.Received += r.Received

				if r.LastHour.After(e.LastHour) {
					e.LastHour = r.LastHour
				}
			}

			internet = append(internet, e)
		}

		slices.SortStableFunc(internet, func(a, b *orgEntry) int {
			return cmp.Compare(b.Sent+b.Received, a.Sent+a.Received)
		})
	}

	return local, internet
}

// filterPeers keeps the peers on service (a serviceChoice key, or empty for
// any) whose names contain query. org is the lowercased organisation the peers
// belong to, which matches all of them.
func filterPeers(peers []*inventory.TrafficPeer, service, query, org string) []*inventory.TrafficPeer {
	var out []*inventory.TrafficPeer

	for _, p := range peers {
		if service != "" && serviceKey(p) != service {
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
	for _, s := range []string{p.DeviceName, p.IP.String(), p.Name, serviceLabel(p)} {
		if strings.Contains(strings.ToLower(s), query) {
			return true
		}
	}

	return false
}

// mergePeers folds per-service rows into one row per peer, busiest first. A
// known device is one peer whatever address it used; anything else is its
// address.
func mergePeers(peers []*inventory.TrafficPeer) []*peerRow {
	type key struct {
		device int64
		ip     netip.Addr
	}

	byPeer := make(map[key]*peerRow)

	var out []*peerRow

	for _, p := range peers {
		k := key{device: p.DeviceID}
		if p.DeviceID == 0 {
			k.ip = p.IP
		}

		r, ok := byPeer[k]
		if !ok {
			r = &peerRow{DeviceID: p.DeviceID, DeviceName: p.DeviceName, IP: p.IP, Name: p.Name}
			byPeer[k] = r
			out = append(out, r)
		}

		r.add(p)
	}

	for _, r := range out {
		slices.SortStableFunc(r.Services, func(a, b *inventory.TrafficPeer) int {
			return cmp.Compare(b.Sent+b.Received, a.Sent+a.Received)
		})
	}

	slices.SortStableFunc(out, func(a, b *peerRow) int {
		return cmp.Compare(b.Sent+b.Received, a.Sent+a.Received)
	})

	return out
}

// groupLocal turns local peers into rows, collapsing two or more unknown
// addresses in one subnet into a single entry.
func groupLocal(peers []*inventory.TrafficPeer) []*localEntry {
	var (
		out     []*localEntry
		byNet   = make(map[netip.Prefix][]*peerRow)
		netList []netip.Prefix
	)

	for _, r := range mergePeers(peers) {
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
			g.Sent += r.Sent
			g.Received += r.Received

			if r.LastHour.After(g.LastHour) {
				g.LastHour = r.LastHour
			}
		}

		out = append(out, &localEntry{Group: g})
	}

	slices.SortStableFunc(out, func(a, b *localEntry) int { return cmp.Compare(b.total(), a.total()) })

	return out
}
