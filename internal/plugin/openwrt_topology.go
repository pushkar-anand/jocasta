package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/pkg/openwrt"
)

// wifiModeAP is the mode of a radio interface that serves an access point.
const wifiModeAP = "ap"

// openWrtTables is every table Topology reads, gathered before any of it is
// mapped, so the mapping can be tested without a router.
type openWrtTables struct {
	identity string
	devices  map[string]openwrt.NetDevice
	radios   []openwrt.Radio
	vlans    []openwrt.BridgeVLAN

	// fdb is each bridge's table, keyed by bridge.
	fdb map[string][]openwrt.FDBEntry

	// stations are the clients of each radio's network, keyed by the device
	// the network runs on.
	stations map[string][]openwrt.Station
}

// Topology reads what is plugged into the router: its ports, the hardware
// addresses each bridge learned on them, and the clients of each radio. A
// table that fails is reported in the error alongside what the other tables
// returned. The Topology is empty only when no table said anything about a
// port.
//
// OpenWrt runs no neighbour discovery protocol by default, so the Topology
// carries no neighbours.
func (o *OpenWrt) Topology(ctx context.Context) (Topology, error) {
	var (
		t    = openWrtTables{fdb: map[string][]openwrt.FDBEntry{}, stations: map[string][]openwrt.Station{}}
		errs []error
	)

	read := func(what string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", what, classifyOpenWrt(err)))
		}
	}

	board, err := o.client.Board(ctx)
	if err == nil {
		t.identity = board.Hostname
	}

	read("board", err)

	t.devices, err = o.client.NetworkDevices(ctx)
	read("network devices", err)

	for _, name := range slices.Sorted(maps.Keys(t.devices)) {
		d := t.devices[name]
		if !d.Bridge {
			continue
		}

		// Entries come back even when a member's port number does not, so
		// both are kept. The error already names the bridge.
		t.fdb[name], err = o.client.BridgeFDB(ctx, name, d.Ports)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %w", classifyOpenWrt(err)))
		}
	}

	t.vlans, err = o.client.BridgeVLANs(ctx)
	read("bridge vlans", err)

	t.radios, err = o.client.WirelessDevices(ctx)
	read("radios", err)

	for _, r := range t.radios {
		for _, i := range r.Interfaces {
			// A station interface, a repeater's or a Wi-Fi uplink's, lists
			// the access point it joined, which is no client of this one.
			if i.Ifname == "" || i.Mode != wifiModeAP {
				continue
			}

			t.stations[i.Ifname], err = o.client.Stations(ctx, i.Ifname)
			read("wifi clients of "+i.Ifname, err)
		}
	}

	learned := false

	for _, entries := range t.fdb {
		learned = learned || len(entries) > 0
	}

	for _, st := range t.stations {
		learned = learned || len(st) > 0
	}

	if !learned && len(errs) > 0 {
		return Topology{}, errors.Join(errs...)
	}

	return o.buildTopology(ctx, t), errors.Join(errs...)
}

// buildTopology maps the tables onto a Topology.
func (o *OpenWrt) buildTopology(ctx context.Context, t openWrtTables) Topology {
	kinds := make(map[string]PortKind, len(t.devices))

	for name, d := range t.devices {
		switch {
		case !d.IsPort():
			kinds[name] = PortVirtual
		case d.IsWireless():
			kinds[name] = PortWiFi
		default:
			kinds[name] = PortWired
		}
	}

	return Topology{
		Identity: t.identity,
		Gateway:  !o.topologyOnly,
		Own:      openWrtOwnMACs(t),
		Ports:    openWrtPorts(t, kinds),
		Seen:     o.openWrtSightings(ctx, t, kinds),
		ReadAt:   o.now(),
	}
}

// openWrtOwnMACs returns the router's own hardware addresses: every device's,
// and every one a bridge marks as local.
func openWrtOwnMACs(t openWrtTables) []string {
	var out []string

	add := func(s string) {
		if mac, ok := hosts.CanonicalMAC(s); ok && mac != "" {
			out = append(out, mac)
		}
	}

	for _, d := range t.devices {
		if !d.IsLoopback() {
			add(d.MAC)
		}
	}

	for _, entries := range t.fdb {
		for _, e := range entries {
			if e.Local {
				add(e.MAC)
			}
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// openWrtPorts returns the router's Ethernet ports and radio interfaces, each
// with its link and the VLANs it carries.
func openWrtPorts(t openWrtTables, kinds map[string]PortKind) []TopologyPort {
	byName := make(map[string]*TopologyPort)

	for name, d := range t.devices {
		if kinds[name] == PortVirtual {
			continue
		}

		p := &TopologyPort{Name: name, Kind: kinds[name], Running: d.Up && d.Link.Carrier}

		if d.Link.Speed > 0 {
			p.Rate = int64(d.Link.Speed) * 1_000_000
			p.FullDuplex = d.Link.Duplex == "full"
		}

		byName[name] = p
	}

	for _, v := range t.vlans {
		for _, vp := range v.Ports {
			p, ok := byName[vp.Name]
			if !ok {
				continue
			}

			if vp.Tagged {
				p.Tagged = append(p.Tagged, v.VLAN)
			} else {
				p.Untagged = append(p.Untagged, v.VLAN)
			}

			if vp.PVID {
				p.PVID = v.VLAN
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

// openWrtSightings returns every hardware address a bridge learned on a port
// and every Wi-Fi client, with the Wi-Fi clients marked. A client the bridge
// has not learned, such as one on a network that is in no bridge, is placed on
// its radio's interface.
func (o *OpenWrt) openWrtSightings(ctx context.Context, t openWrtTables, kinds map[string]PortKind) []Sighting {
	type key struct{ port, mac string }

	// The SSID and band of each radio interface, for its clients.
	type network struct{ ssid, band string }

	networks := make(map[string]network)

	for _, r := range t.radios {
		for _, i := range r.Interfaces {
			if i.Ifname != "" {
				networks[i.Ifname] = network{ssid: i.SSID, band: r.Band}
			}
		}
	}

	seen := make(map[key]Sighting)
	learned := make(map[string][]key)

	for bridge, entries := range t.fdb {
		for _, e := range entries {
			if e.Local {
				continue
			}

			mac, ok := hosts.CanonicalMAC(e.MAC)
			if !ok || mac == "" || e.Port == "" {
				o.logger.DebugContext(ctx, "ignoring a bridge entry that names no device or port",
					slog.String("bridge", bridge),
					slog.String("mac", e.MAC),
				)

				continue
			}

			k := key{e.Port, mac}
			seen[k] = Sighting{Port: e.Port, MAC: mac, WiFi: kinds[e.Port] == PortWiFi}
			learned[mac] = append(learned[mac], k)
		}
	}

	for ifname, stations := range t.stations {
		n := networks[ifname]

		for _, st := range stations {
			mac, ok := hosts.CanonicalMAC(st.MAC)
			if !ok || mac == "" {
				continue
			}

			keys := learned[mac]
			if len(keys) == 0 {
				keys = []key{{ifname, mac}}
				seen[keys[0]] = Sighting{Port: ifname, MAC: mac}
			}

			for _, k := range keys {
				s := seen[k]
				s.WiFi, s.SSID, s.Band = true, n.ssid, bandName(n.band)
				s.TxRate, s.RxRate, s.Signal = st.TxRate, st.RxRate, st.Signal
				seen[k] = s
			}
		}
	}

	return slices.SortedFunc(maps.Values(seen), func(a, b Sighting) int {
		return cmp.Or(strings.Compare(a.Port, b.Port), strings.Compare(a.MAC, b.MAC))
	})
}

// bandName words an OpenWrt band, such as "5g", as RouterOS words its own,
// such as "5ghz", so the topology page reads the same for both.
func bandName(band string) string {
	if band == "" {
		return ""
	}

	return strings.TrimSuffix(band, "g") + "ghz"
}

var _ TopologyReader = (*OpenWrt)(nil)
