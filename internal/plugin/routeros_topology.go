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

// switchTables is every table Topology reads, gathered before any of it is
// mapped, so the mapping can be tested without a router.
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

// Topology reads what is plugged into the router. A table that fails is
// reported in the error alongside what the other tables returned. The
// Topology is empty only when no table said anything about a port.
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

	if len(t.hosts) == 0 && len(t.neighbors) == 0 && len(t.regs) == 0 && len(errs) > 0 {
		return Topology{}, errors.Join(errs...)
	}

	return r.buildTopology(ctx, t), errors.Join(errs...)
}

// buildTopology maps the tables onto a Topology.
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

// portKind sorts an interface type into the three kinds a port is drawn as.
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

// ownMACs returns the router's own hardware addresses: every interface's, and
// every one the bridge marks as local.
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

// buildPorts returns the router's wired ports and bridge members, each with
// the VLANs it carries.
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

	// A radio counts as a port only once it is a bridge member. A router
	// managing access points (CAPsMAN) lists their radios as its own
	// interfaces, but reaches their clients through the access point's port.
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

// sightings returns every hardware address the bridge learned on a port and
// every Wi-Fi client, with the Wi-Fi clients marked. A client the bridge has
// not learned, such as one that has sent nothing since it joined, is placed
// on its radio.
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

	// A client is placed where the bridge learned it, and the radio says it is
	// on Wi-Fi. On a router managing access points the radio is the access
	// point's, and the bridge learned the client on the port the access point
	// hangs from, which is where it belongs.
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

// onWiFi marks s as a client of the radio g describes.
func onWiFi(s Sighting, g routeros.Registration) Sighting {
	s.WiFi, s.SSID, s.Band = true, g.SSID, g.Band
	s.TxRate, s.RxRate = int64(g.TxRate), int64(g.RxRate)
	s.Signal = g.DBM()

	return s
}

// neighbours returns the devices that announced themselves, one per hardware
// address, each on the port it is plugged into and with its IPv4 management
// address when it advertised one.
func (r *RouterOS) neighbours(ctx context.Context, ns []routeros.Neighbor, kinds map[string]PortKind) []Neighbour {
	// A neighbour is heard on every interface its announcement crossed: the
	// port, and the VLAN interface on top of it. Rows are merged per address,
	// and a wired port or radio replaces a VLAN interface.
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

	// A neighbour that announced no address is keyed by its identity, so the
	// identity breaks the tie between two of them on one port.
	slices.SortFunc(out, func(a, b Neighbour) int {
		return cmp.Or(strings.Compare(a.Port, b.Port), strings.Compare(a.MAC, b.MAC), strings.Compare(a.Identity, b.Identity))
	})

	return out
}

// physicalPort returns the wired port or radio among the interfaces a
// neighbour's announcement crossed, or the first of them when none is one.
func physicalPort(n routeros.Neighbor, kinds map[string]PortKind) string {
	for name := range strings.SplitSeq(n.Interface, ",") {
		if k := kinds[strings.TrimSpace(name)]; k == PortWired || k == PortWiFi {
			return strings.TrimSpace(name)
		}
	}

	return n.Port()
}

var _ TopologyReader = (*RouterOS)(nil)
