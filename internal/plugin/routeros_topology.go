package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// switchTables is every table that Topology reads. Topology collects them
// before it maps them. So a test of the mapping needs no router.
type switchTables struct {
	identity  string
	ifaces    []routeros.Interface
	ports     []routeros.BridgePort
	vlans     []routeros.BridgeVLAN
	hosts     []routeros.BridgeHost
	neighbors []routeros.Neighbor
	regs      []routeros.Registration
	links     []routeros.Link
}

// Topology reads what is connected to the router. The error names every table
// that fails.
func (r *RouterOS) Topology(ctx context.Context) (Topology, error) {
	var (
		t    switchTables
		errs []error
	)

	read := func(what string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", what, classifyRouterOS(err)))
		}
	}

	id, err := r.client.Identity(ctx)
	if err == nil {
		t.identity = id.Name
	}

	read("identity", err)

	t.ifaces, err = r.client.Interfaces(ctx)
	read("interfaces", err)

	t.links, err = r.client.Links(ctx, t.ifaces)
	read("links", err)

	t.ports, err = r.client.BridgePorts(ctx)
	read("bridge ports", err)

	t.vlans, err = r.client.BridgeVLANs(ctx)
	read("bridge vlans", err)

	t.hosts, err = r.client.BridgeHosts(ctx)
	read("bridge hosts", err)

	t.neighbors, err = r.client.Neighbors(ctx)
	read("neighbours", err)

	t.regs, err = r.client.Registrations(ctx)
	read("wifi clients", err)

	if err := errors.Join(errs...); err != nil {
		return Topology{}, err
	}

	return r.buildTopology(ctx, t), nil
}

// buildTopology maps the tables to a Topology.
func (r *RouterOS) buildTopology(ctx context.Context, t switchTables) Topology {
	kinds := make(map[string]PortKind, len(t.ifaces))
	for _, i := range t.ifaces {
		kinds[i.Name] = portKind(i.Type)
	}

	return Topology{
		Identity:   t.identity,
		Gateway:    !r.topologyOnly,
		Own:        ownMACs(t),
		Ports:      buildPorts(t, kinds),
		Seen:       r.sightings(ctx, t, kinds),
		Neighbours: r.neighbours(ctx, t.neighbors, kinds),
		ReadAt:     r.now(),
	}
}

// portKind sorts an interface type into the three kinds of drawn port.
func portKind(typ string) PortKind {
	switch typ {
	case "ether":
		return PortWired
	case "wifi", "wlan", "cap":
		return PortWiFi
	default:
		return PortVirtual
	}
}

// ownMACs returns the hardware addresses of the router: the address of every
// interface, and every address that the bridge marks as local.
func ownMACs(t switchTables) []string {
	var out []string

	add := func(s string) {
		if mac, ok := hosts.CanonicalMAC(s); ok && mac != "" {
			out = append(out, mac)
		}
	}

	for _, i := range t.ifaces {
		add(i.MACAddress)
	}

	for _, h := range t.hosts {
		if h.Local {
			add(h.MACAddress)
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// buildPorts returns the wired ports and bridge members of the router, each
// with its VLANs.
func buildPorts(t switchTables, kinds map[string]PortKind) []TopologyPort {
	byName := make(map[string]*TopologyPort)

	port := func(name string) *TopologyPort {
		p, ok := byName[name]
		if !ok {
			p = &TopologyPort{Name: name, Kind: cmp.Or(kinds[name], PortVirtual)}
			byName[name] = p
		}

		return p
	}

	// A radio counts as a port only when it is a bridge member. A router that
	// manages access points (CAPsMAN) lists their radios as its own
	// interfaces. But it reaches their clients through the port of the access
	// point.
	for _, i := range t.ifaces {
		if i.Disabled || kinds[i.Name] != PortWired {
			continue
		}

		port(i.Name).Running = bool(i.Running)
	}

	for _, l := range t.links {
		if p, ok := byName[l.Name]; ok && l.Rate > 0 {
			p.Rate, p.Capable, p.FullDuplex = int64(l.Rate), int64(l.Capable()), bool(l.FullDuplex)
		}
	}

	for _, bp := range t.ports {
		if bp.Disabled || bp.Interface == "" {
			continue
		}

		p := port(bp.Interface)
		p.PVID, _ = bp.VLAN()
		p.Running = p.Running || !bool(bp.Inactive)
	}

	for _, v := range t.vlans {
		if v.Disabled {
			continue
		}

		tags := v.VLANs()

		for _, name := range v.TaggedPorts() {
			if p, ok := byName[name]; ok {
				p.Tagged = append(p.Tagged, tags...)
			}
		}

		for _, name := range v.UntaggedPorts() {
			if p, ok := byName[name]; ok {
				p.Untagged = append(p.Untagged, tags...)
			}
		}
	}

	out := make([]TopologyPort, 0, len(byName))

	for _, p := range byName {
		slices.Sort(p.Tagged)
		slices.Sort(p.Untagged)

		p.Tagged = slices.Compact(p.Tagged)
		p.Untagged = slices.Compact(p.Untagged)

		out = append(out, *p)
	}

	slices.SortFunc(out, func(a, b TopologyPort) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// sightings returns every hardware address that the bridge learned on a port,
// and every Wi-Fi client. It marks the Wi-Fi clients. A client that the bridge
// did not learn goes on its radio. An example is a client that sent nothing
// since it joined.
func (r *RouterOS) sightings(ctx context.Context, t switchTables, kinds map[string]PortKind) []Sighting {
	type key struct {
		port, mac string
		vlan      int
	}

	regs := make(map[string]routeros.Registration, len(t.regs))

	for _, g := range t.regs {
		if mac, ok := hosts.CanonicalMAC(g.MACAddress); ok && mac != "" {
			regs[mac] = g
		}
	}

	pvid := make(map[string]int, len(t.ports))

	for _, bp := range t.ports {
		pvid[bp.Interface], _ = bp.VLAN()
	}

	seen := make(map[key]Sighting, len(t.hosts)+len(t.regs))
	learned := make(map[string][]key, len(t.hosts))

	for _, h := range t.hosts {
		if h.Local || h.Invalid || h.Disabled {
			continue
		}

		mac, ok := hosts.CanonicalMAC(h.MACAddress)
		if !ok || mac == "" || h.Port() == "" {
			r.logger.DebugContext(ctx, "ignoring a bridge host that names no device or port",
				slog.String("mac", h.MACAddress),
				slog.String("port", h.Port()),
			)

			continue
		}

		s := Sighting{Port: h.Port(), MAC: mac, WiFi: kinds[h.Port()] == PortWiFi}
		s.VLAN, _ = h.VLAN()

		seen[key{s.Port, s.MAC, s.VLAN}] = s
		learned[mac] = append(learned[mac], key{s.Port, s.MAC, s.VLAN})
	}

	// A client goes where the bridge learned it. The radio says that it is on
	// Wi-Fi. On a router that manages access points, the radio belongs to the
	// access point. The bridge learned the client on the port from which the
	// access point hangs. This is the right place.
	for mac, g := range regs {
		keys := learned[mac]

		if len(keys) == 0 {
			if g.Interface == "" {
				continue
			}

			s := Sighting{Port: g.Interface, MAC: mac, VLAN: pvid[g.Interface]}
			seen[key{s.Port, s.MAC, s.VLAN}] = onWiFi(s, g)

			continue
		}

		for _, k := range keys {
			seen[k] = onWiFi(seen[k], g)
		}
	}

	return slices.SortedFunc(maps.Values(seen), func(a, b Sighting) int {
		return cmp.Or(strings.Compare(a.Port, b.Port), strings.Compare(a.MAC, b.MAC), cmp.Compare(a.VLAN, b.VLAN))
	})
}

// onWiFi marks s as a client of the radio that g describes.
func onWiFi(s Sighting, g routeros.Registration) Sighting {
	s.WiFi, s.SSID, s.Band = true, g.SSID, g.Band
	s.TxRate, s.RxRate = int64(g.TxRate), int64(g.RxRate)
	s.Signal = g.DBM()

	return s
}

// neighbours returns the devices that announced themselves, one for each
// hardware address. Each is on the port that it connects to. Each has its IPv4
// management address, if it advertised one.
func (r *RouterOS) neighbours(ctx context.Context, ns []routeros.Neighbor, kinds map[string]PortKind) []Neighbour {
	// The router hears a neighbour on every interface of its announcement:
	// the port, and the VLAN interface on top of it. Rows merge by address. A
	// wired port or radio replaces a VLAN interface.
	byMAC := make(map[string]*Neighbour, len(ns))

	for _, n := range ns {
		mac, ok := hosts.CanonicalMAC(n.MACAddress)
		if !ok || n.Port() == "" || (mac == "" && n.Identity == "") {
			r.logger.DebugContext(ctx, "ignoring a neighbour that names no device or port",
				slog.String("mac", n.MACAddress),
				slog.String("interface", n.Interface),
			)

			continue
		}

		id := cmp.Or(mac, n.Identity)
		port := physicalPort(n, kinds)

		nb, ok := byMAC[id]
		if !ok {
			nb = &Neighbour{Port: port, MAC: mac}
			byMAC[id] = nb
		}

		if kinds[nb.Port] == PortVirtual && kinds[port] != PortVirtual {
			nb.Port = port
		}

		nb.Identity = cmp.Or(nb.Identity, n.Identity)
		nb.Platform = cmp.Or(nb.Platform, n.Platform)
		nb.Board = cmp.Or(nb.Board, n.Board)
		nb.TheirPort = cmp.Or(nb.TheirPort, n.InterfaceName)

		if a, err := netip.ParseAddr(n.Addr()); err == nil && !a.IsLinkLocalUnicast() && !nb.Addr.Is4() {
			nb.Addr = a
		}
	}

	out := make([]Neighbour, 0, len(byMAC))
	for _, nb := range byMAC {
		out = append(out, *nb)
	}

	// The key of a neighbour with no address is its identity. So the identity
	// breaks the tie between two such neighbours on one port.
	slices.SortFunc(out, func(a, b Neighbour) int {
		return cmp.Or(strings.Compare(a.Port, b.Port), strings.Compare(a.MAC, b.MAC), strings.Compare(a.Identity, b.Identity))
	})

	return out
}

// physicalPort returns the wired port or radio among the interfaces of the
// announcement of a neighbour. If none is, it returns the first interface.
func physicalPort(n routeros.Neighbor, kinds map[string]PortKind) string {
	for name := range strings.SplitSeq(n.Interface, ",") {
		if k := kinds[strings.TrimSpace(name)]; k == PortWired || k == PortWiFi {
			return strings.TrimSpace(name)
		}
	}

	return n.Port()
}

var _ TopologyReader = (*RouterOS)(nil)
