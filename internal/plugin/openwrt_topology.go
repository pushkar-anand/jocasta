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

// openWrtTables is every table that Topology reads. Topology collects them
// before it maps them. So a test of the mapping needs no router.
type openWrtTables struct {
	identity string
	devices  map[string]openwrt.NetDevice
	radios   []openwrt.Radio
	vlans    []openwrt.BridgeVLAN

	// fdb is the table of each bridge, with the bridge as key.
	fdb map[string][]openwrt.FDBEntry

	// stations are the clients of the network of each radio. The key is the
	// device on which the network runs.
	stations map[string][]openwrt.Station
}

// Topology reads what is connected to the router: its ports, the hardware
// addresses that each bridge learned on them, and the clients of each radio.
// The error names every table that fails.
//
// By default, OpenWrt runs no neighbour discovery protocol. So the Topology has
// no neighbours.
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

		// The error already names the bridge.
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
			// A station interface, of a repeater or a Wi-Fi uplink, lists the
			// access point that it joined. That access point is not a client
			// of this one.
			if i.Ifname == "" || i.Mode != wifiModeAP {
				continue
			}

			t.stations[i.Ifname], err = o.client.Stations(ctx, i.Ifname)
			read("wifi clients of "+i.Ifname, err)
		}
	}

	if err := errors.Join(errs...); err != nil {
		return Topology{}, err
	}

	return o.buildTopology(ctx, t), nil
}

// buildTopology maps the tables to a Topology.
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

// openWrtOwnMACs returns the hardware addresses of the router: the address of
// every device, and every address that a bridge marks as local.
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

// openWrtPorts returns the Ethernet ports and radio interfaces of the router,
// each with its link and its VLANs.
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

// openWrtSightings returns every hardware address that a bridge learned on a
// port, and every Wi-Fi client. It marks the Wi-Fi clients. A client that the
// bridge did not learn goes on the interface of its radio. An example is a
// client on a network that is in no bridge.
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

// bandName writes an OpenWrt band, such as "5g", as RouterOS writes its band,
// such as "5ghz". So the topology page is the same for both.
func bandName(band string) string {
	if band == "" {
		return ""
	}

	return strings.TrimSuffix(band, "g") + "ghz"
}

var _ TopologyReader = (*OpenWrt)(nil)
