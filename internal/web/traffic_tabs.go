package web

import (
	"cmp"
	"net/netip"
	"strconv"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// Tab keys that are not a recorded network's.
const (
	// tabInternet holds every public address, by organisation.
	tabInternet = "internet"

	// tabElsewhere holds local addresses on no recorded network: a network
	// the router never reported, or every local address when none is
	// recorded.
	tabElsewhere = "local"

	// tabBroadcasts holds what the device sent to everyone.
	tabBroadcasts = "broadcasts"
)

// trafficTab is one segment of a device's "Talks to" section: a recorded
// network, the rest of the local addresses, or the internet.
type trafficTab struct {
	Key   string
	Label string
	VLAN  int

	// Local are the rows of a local tab, Internet the rows of the Internet
	// tab, Broadcasts the rows of the Broadcasts tab.
	Local      []*localEntry
	Internet   []*orgEntry
	Broadcasts []*broadcastRow

	// Count is how many peers the tab holds: devices and addresses on a local
	// tab, organisations on the Internet tab.
	Count int

	prefix     netip.Prefix
	bytes      int64
	tries      int64
	stagePeers []*inventory.TrafficPeer
	stageTried []*inventory.Attempt
}

// IsInternet reports whether the tab is the internet one.
func (t *trafficTab) IsInternet() bool { return t.Key == tabInternet }

// IsBroadcasts reports whether the tab is the broadcasts one.
func (t *trafficTab) IsBroadcasts() bool { return t.Key == tabBroadcasts }

// Empty reports whether the tab has no rows.
func (t *trafficTab) Empty() bool { return t.Count == 0 }

func networkTabKey(id int64) string { return "net-" + strconv.FormatInt(id, 10) }

// segmentTabs splits a device's traffic and tries, both already filtered, into
// one tab per recorded network, one for local addresses on none of them, and
// one for the internet. A recorded network the device reached nothing on is
// still a tab, so the tabs read the same from one device to the next; the
// leftover local tab is only there when something is in it.
func segmentTabs(
	nets []*inventory.Network, local []*inventory.TrafficPeer, orgs []*inventory.TrafficOrg,
	tried []*inventory.Attempt, f trafficFilter,
) []*trafficTab {
	var tabs []*trafficTab

	for _, n := range nets {
		pfx, err := netip.ParsePrefix(n.CIDR)
		if err != nil {
			continue
		}

		tabs = append(tabs, &trafficTab{
			Key: networkTabKey(n.ID), Label: n.Name, VLAN: n.VLAN, prefix: pfx.Masked(),
		})

		if n.Name == "" {
			tabs[len(tabs)-1].Label = pfx.Masked().String()
		}
	}

	elsewhere := &trafficTab{Key: tabElsewhere, Label: "Elsewhere on your network"}
	if len(tabs) == 0 {
		elsewhere.Label = "On your network"
	}

	// The narrowest recorded network holding an address is its segment, so
	// a /24 carved out of a /16 wins over the /16.
	segmentOf := func(ip netip.Addr) *trafficTab {
		var best *trafficTab

		for _, t := range tabs {
			if t.prefix.Contains(ip) && (best == nil || t.prefix.Bits() > best.prefix.Bits()) {
				best = t
			}
		}

		if best == nil {
			return elsewhere
		}

		return best
	}

	for _, p := range local {
		t := segmentOf(p.IP)
		t.stagePeers = append(t.stagePeers, p)
	}

	var internetTried []*inventory.Attempt

	for _, a := range tried {
		if a.Internet {
			internetTried = append(internetTried, a)

			continue
		}

		t := segmentOf(a.IP)
		t.stageTried = append(t.stageTried, a)
	}

	if len(tabs) == 0 || len(elsewhere.stagePeers)+len(elsewhere.stageTried) > 0 {
		tabs = append(tabs, elsewhere)
	}

	for _, t := range tabs {
		t.Local = groupLocal(t.stagePeers, t.stageTried)

		for _, e := range t.Local {
			if e.Group != nil {
				t.Count += len(e.Group.Peers)
			} else {
				t.Count++
			}

			b, n := e.order()
			t.bytes += b
			t.tries += n
		}

		t.stagePeers, t.stageTried = nil, nil
	}

	internet := &trafficTab{Key: tabInternet, Label: "Internet"}
	internet.Internet = groupInternet(orgs, internetTried, f.Service, strings.ToLower(f.Query))
	internet.Count = len(internet.Internet)

	for _, o := range internet.Internet {
		internet.bytes += o.Sent + o.Received
		internet.tries += o.Tries
	}

	return append(tabs, internet)
}

// broadcastRow is one broadcast as the Broadcasts tab reads it.
type broadcastRow struct {
	*inventory.Broadcast

	// What is the protocol it most likely is, or the port it went to; To
	// is who it went to.
	What string
	To   string
}

// broadcastTab is the tab for what the device sent to everyone, after the
// service and query filter. nets name the subnet a broadcast went to.
func broadcastTab(nets []*inventory.Network, all []*inventory.Broadcast, f trafficFilter) *trafficTab {
	t := &trafficTab{Key: tabBroadcasts, Label: "Broadcasts"}
	query := strings.ToLower(f.Query)
	protocol, port, hasService := parseServiceKey(f.Service)

	for _, b := range all {
		if hasService && (b.Protocol != protocol || b.Port != port) {
			continue
		}

		r := &broadcastRow{Broadcast: b, What: broadcastWhat(b), To: broadcastTo(b, nets)}

		if query != "" && !strings.Contains(strings.ToLower(r.What+" "+r.To+" "+b.Dst.String()), query) {
			continue
		}

		t.Broadcasts = append(t.Broadcasts, r)
		t.bytes += b.Bytes
	}

	t.Count = len(t.Broadcasts)

	return t
}

// broadcastWhat is what a broadcast most likely was, or where it went when
// nothing says.
func broadcastWhat(b *inventory.Broadcast) string {
	if b.Service != "" {
		return b.Service
	}

	proto := cmp.Or(protoName(b.Protocol), "TCP")
	if b.Port == 0 {
		return proto
	}

	return proto + " port " + strconv.Itoa(int(b.Port))
}

// broadcastTo says who a broadcast reached.
func broadcastTo(b *inventory.Broadcast, nets []*inventory.Network) string {
	switch b.Kind {
	case "subnet":
		for _, n := range nets {
			if pfx, err := netip.ParsePrefix(n.CIDR); err == nil && pfx.Contains(b.Dst) {
				return "everyone on " + cmp.Or(n.Name, pfx.Masked().String())
			}
		}

		return "everyone on its subnet"
	case "all":
		return "everyone on its segment"
	}

	return "multicast group"
}

// pickTab is the tab key names, or the busiest when key names none: most
// data moved, then most tries, then the first with anything in it.
func pickTab(tabs []*trafficTab, key string) *trafficTab {
	for _, t := range tabs {
		if t.Key == key {
			return t
		}
	}

	best := tabs[0]

	for _, t := range tabs[1:] {
		if busier(t.bytes, t.tries, best.bytes, best.tries) < 0 ||
			(best.Count == 0 && t.Count > 0) {
			best = t
		}
	}

	return best
}

// trafficSummary is a device's traffic over the window at a glance, before
// any filter.
type trafficSummary struct {
	Sent, Received int64

	// Local is how many devices and addresses on the network it talked to,
	// Orgs how many organisations on the internet.
	Local int
	Orgs  int

	// Tries and Answered count the connections that carried nothing, and
	// TriedPeers how many peers they went to.
	Tries, Answered int64
	TriedPeers      int

	// FromInternet counts the connections internet peers opened on the
	// device.
	FromInternet int64
}

func summarise(t *inventory.DeviceTraffic, attempts []*inventory.Attempt) trafficSummary {
	var s trafficSummary

	for _, r := range mergePeers(t.Local, nil) {
		s.Sent += r.Sent
		s.Received += r.Received
		s.Local++
	}

	for _, o := range t.Internet {
		s.Sent += o.Sent
		s.Received += o.Received

		for _, p := range o.Peers {
			s.FromInternet += p.ConnectionsIn
		}
	}

	s.Orgs = len(t.Internet)

	for _, a := range attempts {
		s.Tries += a.Attempts
		s.Answered += a.Answered
	}

	s.TriedPeers = len(mergePeers(nil, attempts))

	return s
}
