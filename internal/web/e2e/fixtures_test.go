//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// anchor is the moment every fixture is built back from and every page is
// served at, so a golden shows the same "4m ago" on every run.
var anchor = time.Date(2026, time.June, 15, 14, 30, 0, 0, time.UTC)

// Addresses come from RFC 5737 and RFC 3849, hardware addresses from RFC 7042.
// Internet peers have to be public for their organisation and country to
// resolve, so they are public DNS resolvers and root servers, which anyone can
// look up.
const (
	router  = "gateway"
	sweeper = "sweep:jocasta"
)

var (
	exporter = netip.MustParseAddr("192.0.2.1")
	outside  = netip.MustParseAddr("203.0.113.7")
)

const (
	cloudflare  = "1.1.1.1"
	cloudflare2 = "1.0.0.1"
	google      = "8.8.8.8"
	google2     = "8.8.4.4"
	quad9       = "9.9.9.9"
	opendns     = "208.67.222.222"
	adguard     = "94.140.14.14"
	rootA       = "198.41.0.4"
	rootC       = "192.33.4.12"
	rootD       = "199.7.91.13"
	rootF       = "192.5.5.241"
	rootI       = "192.36.148.17"
	rootJ       = "192.58.128.30"
	rootK       = "193.0.14.129"
	rootL       = "199.7.83.42"
	rootM       = "202.12.27.33"
)

// probers knock on the outside address carrying nothing.
var probers = []string{rootC, rootF, rootI, rootM}

// fixture is one state of the inventory a page can be shown in.
type fixture struct {
	name string

	// seed fills the inventory; nil leaves it empty.
	seed func(ctx context.Context, store *inventory.Store, conn *sql.DB, log *slog.Logger) (*inventory.TrafficRecorder, error)

	// noUsers leaves the account table empty, for the first-run setup page.
	noUsers bool
}

var (
	fixtureEmpty  = fixture{name: "empty"}
	fixtureNormal = fixture{name: "normal", seed: seedNormal}
	fixtureWeird  = fixture{name: "weird", seed: seedWeird}
	fixtureFresh  = fixture{name: "fresh", noUsers: true}
)

type device struct {
	ip, mac, name string
	label, group  string
	kind          string
	notes         string

	// firstDay is the day, counted back from the anchor, the device first
	// appeared; lastDay the day it was last seen (0 = still here).
	firstDay, lastDay int

	ports    []uint16
	services []hosts.Service

	// movedTo is an address the device took from DHCP movedDay days ago.
	movedTo  string
	movedDay int

	watched bool
	curated bool

	talks []talk
}

type talk struct {
	peer     string
	port     uint16
	proto    uint8
	kbPerHr  float64
	upRatio  float64
	sinceDay int
	inbound  bool
}

func tcp(peer string, port uint16, kb float64) talk {
	return talk{peer: peer, port: port, proto: 6, kbPerHr: kb, upRatio: 0.08}
}

func udp(peer string, port uint16, kb float64) talk {
	return talk{peer: peer, port: port, proto: 17, kbPerHr: kb, upRatio: 0.5}
}

// sim is what a fixture simulates over its days.
type sim struct {
	days     int
	nets     []plugin.Network
	swept    netip.Prefix
	devs     []*device
	topology func(ctx context.Context, store *inventory.Store, devs []*device) error

	// scanFrom is the device that ran a port scan of a segment a few hours
	// back; empty for none.
	scanFrom string
	scanOf   netip.Prefix
}

func seedNormal(ctx context.Context, store *inventory.Store, conn *sql.DB, log *slog.Logger) (*inventory.TrafficRecorder, error) {
	home := netip.MustParsePrefix("192.0.2.0/25")
	servers := netip.MustParsePrefix("192.0.2.128/25")
	iot := netip.MustParsePrefix("198.51.100.0/24")

	n := &sim{
		days: 30,
		nets: []plugin.Network{
			{Prefix: home, Name: "Home", VLAN: 1},
			{Prefix: servers, Name: "Servers", VLAN: 10},
			{Prefix: iot, Name: "IoT", VLAN: 20},
		},
		swept:    home,
		devs:     normalDevices(),
		topology: normalTopology,
		scanFrom: "192.0.2.10",
		scanOf:   iot,
	}

	rec, err := simulate(ctx, store, log, n)
	if err != nil {
		return nil, err
	}

	return rec, failedScan(ctx, conn, router, "dial 192.0.2.1:8728: connection refused", 2*time.Hour)
}

func normalDevices() []*device {
	return []*device{
		{ip: "192.0.2.1", mac: "00:00:5e:00:53:01", name: "gateway", label: "Gateway", group: "Network", kind: "router", ports: []uint16{22, 53, 80, 443, 8291}},
		{ip: "192.0.2.2", mac: "00:00:5e:00:53:02", name: "switch-core", label: "Core switch", group: "Network", kind: "switch", ports: []uint16{22, 80}},
		{ip: "192.0.2.3", mac: "00:00:5e:00:53:03", name: "ap-hallway", label: "Hallway AP", group: "Network", kind: "access_point", ports: []uint16{22, 443}},
		{ip: "192.0.2.10", mac: "00:00:5e:00:53:10", name: "workstation", label: "Workstation", group: "Office", kind: "desktop", ports: []uint16{22}, watched: true,
			talks: []talk{tcp(cloudflare, 443, 2400), tcp(google, 443, 5200), tcp(quad9, 443, 1400), udp(cloudflare, 53, 40), tcp("192.0.2.130", 445, 8000), tcp("192.0.2.132", 8123, 150)}},
		{ip: "192.0.2.11", mac: "00:00:5e:00:53:11", name: "laptop", label: "Laptop", group: "Office", kind: "laptop",
			talks: []talk{tcp(google, 443, 2600), tcp(opendns, 443, 900), tcp(adguard, 443, 700), udp(cloudflare, 53, 20), tcp("192.0.2.131", 8096, 4000)}},
		{ip: "192.0.2.20", mac: "00:00:5e:00:53:20", name: "phone-a", movedTo: "192.0.2.24", movedDay: 1, label: "Phone A", group: "Personal", kind: "phone",
			talks: []talk{tcp(google, 443, 1200), tcp(rootK, 443, 1600), tcp(cloudflare2, 443, 500)}},
		{ip: "192.0.2.21", mac: "00:00:5e:00:53:21", name: "phone-b", label: "Phone B", group: "Personal", kind: "phone",
			talks: []talk{tcp(google2, 443, 1500), tcp(rootM, 443, 600)}},
		{ip: "192.0.2.22", mac: "00:00:5e:00:53:22", name: "tablet", label: "Tablet", group: "Personal", kind: "tablet", lastDay: 3,
			talks: []talk{tcp(rootJ, 443, 700)}},
		{ip: "192.0.2.30", mac: "00:00:5e:00:53:30", name: "tv-living-room", label: "Living room TV", group: "Media", kind: "tv",
			services: []hosts.Service{{Type: "_googlecast._tcp", Instance: "Living Room TV", Port: 8009}, {Type: "_airplay._tcp", Instance: "Living Room", Port: 7000}},
			talks:    []talk{tcp(rootL, 443, 38000), tcp(google, 443, 4000), tcp("192.0.2.131", 8096, 9000)}},
		{ip: "192.0.2.31", mac: "00:00:5e:00:53:31", name: "console", label: "Game console", group: "Media", kind: "game_console", firstDay: 5,
			talks: []talk{tcp(rootD, 443, 9000), udp(rootA, 3074, 600)}},
		{ip: "192.0.2.40", mac: "00:00:5e:00:53:40", name: "printer", label: "Printer", group: "Office", kind: "printer", ports: []uint16{80, 443, 631, 9100},
			services: []hosts.Service{{Type: "_ipp._tcp", Instance: "Office Printer", Port: 631}}},

		{ip: "192.0.2.130", mac: "00:00:5e:00:53:80", name: "nas", label: "NAS", group: "Servers", kind: "nas", ports: []uint16{22, 80, 443, 445, 2049, 5000},
			notes: "Backups run nightly at 02:00.\nDisk 3 replaced in May.",
			talks: []talk{tcp(rootA, 443, 1500), tcp(rootI, 80, 200), udp(cloudflare, 123, 2),
				{peer: rootK, port: 443, proto: 6, kbPerHr: 200, upRatio: 12, inbound: true},
				{peer: rootJ, port: 443, proto: 6, kbPerHr: 120, upRatio: 10, inbound: true, sinceDay: 6}}},
		{ip: "192.0.2.131", mac: "00:00:5e:00:53:81", name: "media-server", label: "Media server", group: "Servers", kind: "server", ports: []uint16{22, 8096},
			talks: []talk{tcp(google, 443, 400), tcp(rootF, 443, 2000), udp(cloudflare, 123, 2)}},
		{ip: "192.0.2.132", mac: "00:00:5e:00:53:82", name: "home-assistant", label: "Home Assistant", group: "Servers", kind: "iot_hub", ports: []uint16{22, 1883, 8123}, watched: true,
			talks: []talk{tcp(quad9, 443, 150), tcp("198.51.100.10", 80, 40), tcp("198.51.100.14", 80, 20)}},
		{ip: "192.0.2.133", mac: "00:00:5e:00:53:83", name: "dns", label: "DNS resolver", group: "Servers", kind: "server", ports: []uint16{22, 53, 80},
			talks: []talk{udp(cloudflare, 53, 300), udp(google, 53, 200), udp(quad9, 53, 100)}},
		{ip: "192.0.2.134", mac: "00:00:5e:00:53:84", name: "backup", label: "Backup box", group: "Servers", kind: "server", ports: []uint16{22}, firstDay: 12,
			talks: []talk{tcp(rootM, 22, 200), tcp("192.0.2.130", 2049, 12000)}},
		{ip: "192.0.2.135", mac: "00:00:5e:00:53:85", name: "hypervisor", label: "Hypervisor", group: "Servers", kind: "hypervisor", ports: []uint16{22, 443, 8006},
			talks: []talk{tcp(rootI, 80, 800), tcp(rootL, 443, 1200)}},

		{ip: "198.51.100.10", mac: "00:00:5e:00:53:a0", name: "thermostat", label: "Thermostat", group: "Smart home", kind: "smart_home",
			talks: []talk{tcp(google2, 443, 30)}},
		{ip: "198.51.100.11", mac: "00:00:5e:00:53:a1", name: "doorbell", label: "Doorbell", group: "Smart home", kind: "camera",
			talks: []talk{tcp(rootA, 443, 900), udp(rootA, 3478, 300)}},
		{ip: "198.51.100.12", mac: "00:00:5e:00:53:a2", name: "camera-garage", label: "Garage camera", group: "Cameras", kind: "camera", ports: []uint16{80, 554},
			talks: []talk{tcp(rootM, 443, 700), {peer: adguard, port: 443, proto: 6, kbPerHr: 40, upRatio: 3, sinceDay: 4}}},
		{ip: "198.51.100.13", mac: "00:00:5e:00:53:a3", name: "camera-porch", label: "Porch camera", group: "Cameras", kind: "camera", ports: []uint16{80, 554},
			talks: []talk{tcp(rootM, 443, 600)}},
		{ip: "198.51.100.14", mac: "00:00:5e:00:53:a4", name: "plug-kitchen", label: "Kitchen plug", group: "Smart home", kind: "smart_home",
			talks: []talk{tcp(rootD, 8883, 5)}},
		{ip: "198.51.100.16", mac: "00:00:5e:00:53:a6", name: "speaker-kitchen", label: "Kitchen speaker", group: "Media", kind: "speaker",
			talks: []talk{tcp(google, 443, 700)}},
		{ip: "198.51.100.17", mac: "00:00:5e:00:53:a7", name: "vacuum", label: "Robot vacuum", group: "Smart home", kind: "smart_home", firstDay: 9,
			talks: []talk{tcp(rootM, 8883, 15)}},
		{ip: "198.51.100.18", mac: "00:00:5e:00:53:a8", name: "light-strip", group: "Smart home", kind: "smart_home",
			talks: []talk{tcp(rootD, 8883, 3)}},
		{ip: "198.51.100.19", mac: "00:00:5e:00:53:a9", firstDay: 1},
		{ip: "198.51.100.20", mac: "00:00:5e:00:53:aa", name: "air-purifier", label: "Air purifier", group: "Smart home", kind: "smart_home", lastDay: 8,
			talks: []talk{tcp(rootM, 443, 8)}},
	}
}

// seedWeird is the inventory a layout has to survive: names at their length
// limits and in every script, markup in user text, a device with every port
// open, more devices and groups than a page holds, and absurd traffic.
func seedWeird(ctx context.Context, store *inventory.Store, conn *sql.DB, log *slog.Logger) (*inventory.TrafficRecorder, error) {
	main := netip.MustParsePrefix("192.0.2.0/24")
	iot := netip.MustParsePrefix("198.51.100.0/24")
	bare := netip.MustParsePrefix("203.0.113.0/24")

	n := &sim{
		days: 10,
		nets: []plugin.Network{
			{Prefix: main, Name: "Main floor, garage extension and the wired half of the loft", VLAN: 4094},
			{Prefix: iot, Name: "Ügéñ 物联网 🏠", VLAN: 20},
			{Prefix: bare},
		},
		swept:    main,
		devs:     weirdDevices(),
		topology: weirdTopology,
		scanFrom: "192.0.2.2",
		scanOf:   iot,
	}

	rec, err := simulate(ctx, store, log, n)
	if err != nil {
		return nil, err
	}

	if err := failedScan(ctx, conn, router, strings.Repeat("read /ip/arp: unexpected reply from 192.0.2.1 ", 12), time.Hour); err != nil {
		return nil, err
	}

	if err := failedScan(ctx, conn, sweeper, "sweep 192.0.2.0/24: operation not permitted", 30*time.Minute); err != nil {
		return nil, err
	}

	return rec, nil
}

var (
	longLabel   = "Upstairs study desktop that also runs the backup jobs on weekday nights and the media transcode queue at the weekend"
	unbroken    = strings.Repeat("x", 120)
	markup      = `<img src=x onerror=alert(1)> & "quoted" 'single' </td>`
	longHost    = "a-very-long-hostname-label-that-reaches-the-sixty-three-limit-x"
	longFQDN    = longHost + "." + longHost + ".example.com"
	longGroup   = "Devices in the garden office that the neighbour also uses on weekends"
	longService = "Living Room Television (Second Floor) with the Soundbar and the Projector"
	longNotes   = strings.TrimSpace(strings.Repeat("Replaced the power supply after it browned out during the storm. ", 20)) +
		"\n\nManual: https://example.com/" + strings.Repeat("very-long-path-segment/", 10) + "manual.pdf\n\n" +
		strings.Repeat("line\n", 15)
)

func weirdDevices() []*device {
	manyPorts := make([]uint16, 0, 160)
	for p := uint16(1); len(manyPorts) < 150; p += 7 {
		manyPorts = append(manyPorts, p)
	}

	manyServices := make([]hosts.Service, 0, 30)
	for i := range 30 {
		manyServices = append(manyServices, hosts.Service{
			Type: fmt.Sprintf("_service-type-%02d._tcp", i), Instance: fmt.Sprintf("%s %d", longService, i), Port: uint16(8000 + i),
		})
	}

	busy := make([]talk, 0, len(allPeers)*4)
	for i, p := range allPeers {
		for _, port := range []uint16{443, 80, 8443, uint16(10000 + i)} {
			busy = append(busy, tcp(p, port, 400))
		}
	}

	devs := []*device{
		{ip: "192.0.2.1", mac: "00:00:5e:00:53:01", name: router, label: "Gateway", group: "Network", kind: "router", ports: []uint16{22, 53, 80, 443}},
		{ip: "192.0.2.2", mac: "00:00:5e:00:53:02", name: longHost, label: longLabel, group: longGroup, kind: "desktop", notes: longNotes, watched: true,
			ports: manyPorts, services: manyServices, talks: busy},
		{ip: "192.0.2.3", mac: "00:00:5e:00:53:03", name: longFQDN, kind: "server"},
		{ip: "192.0.2.4", mac: "00:00:5e:00:53:04", name: "unbroken", label: unbroken, group: unbroken[:100], kind: "nas", notes: unbroken + unbroken},
		{ip: "192.0.2.5", mac: "00:00:5e:00:53:05", name: "markup", label: markup, group: markup, notes: markup, kind: "phone",
			talks: []talk{tcp(google, 443, 8e9)}},
		{ip: "192.0.2.6", mac: "00:00:5e:00:53:06", name: "emoji", label: "📺 TV 🔊🎮👾🛰️", group: "🏠 Home", kind: "tv"},
		{ip: "192.0.2.7", mac: "00:00:5e:00:53:07", name: "rtl", label: "جهاز المطبخ الذكي", group: "مطبخ", kind: "smart_home"},
		{ip: "192.0.2.8", mac: "00:00:5e:00:53:08", name: "cjk", label: "客厅的电视机顶盒和音响系统", group: "客厅", kind: "tv"},
		{ip: "192.0.2.9", mac: "00:00:5e:00:53:09"},
		{ip: "192.0.2.10", mac: "00:00:5e:00:53:0a", name: "ancient", label: "Seen once, long ago", firstDay: 10, lastDay: 9},
		{ip: "192.0.2.11", mac: "00:00:5e:00:53:0b", name: "mover", label: "Moves every day", movedTo: "192.0.2.250", movedDay: 2},
		{ip: "192.0.2.12", mac: "00:00:5e:00:53:0c", name: "huge", label: "Huge transfer", kind: "server",
			talks: []talk{tcp(cloudflare, 443, 9e11), {peer: quad9, port: 22, proto: 6, kbPerHr: 3e9, upRatio: 40, inbound: true}}},
	}

	// Enough of the rest to page the device list, spread over more groups
	// than any filter list is drawn for.
	for i := 13; len(devs) < 240; i++ {
		ip := fmt.Sprintf("192.0.2.%d", i)
		if i > 200 {
			ip = fmt.Sprintf("198.51.100.%d", i-190)
		}

		devs = append(devs, &device{
			ip: ip, mac: fmt.Sprintf("00:00:5e:00:53:%02x", i), name: fmt.Sprintf("host-%03d", i),
			label: fmt.Sprintf("Device %d", i), group: fmt.Sprintf("Group %02d", i%45),
			kind:    []string{"phone", "laptop", "smart_home", "camera", "", "tv"}[i%6],
			lastDay: []int{0, 0, 0, 2, 0, 7}[i%6],
		})
	}

	return devs
}

var allPeers = []string{cloudflare, cloudflare2, google, google2, quad9, opendns, adguard, rootA, rootC, rootD, rootF, rootI, rootJ, rootK, rootL, rootM}

// simulate walks the network's days an hour at a time: a router read every
// six hours, a sweep of the one segment the sweeper sits on every six hours
// offset by three, traffic every hour for the last fortnight.
func simulate(ctx context.Context, store *inventory.Store, log *slog.Logger, n *sim) (*inventory.TrafficRecorder, error) {
	if err := store.RecordNetworks(ctx, n.nets); err != nil {
		return nil, err
	}

	rng := rand.New(rand.NewPCG(7, 11)) //nolint:gosec // a fixed seed, so every run fabricates the same network.
	rec := inventory.NewTrafficRecorder(store, log, nil)
	start := anchor.Add(-time.Duration(n.days) * 24 * time.Hour)

	local := func(a netip.Addr) bool {
		for _, nw := range n.nets {
			if nw.Prefix.Contains(a) {
				return true
			}
		}

		return false
	}

	for t := start; t.Before(anchor); t = t.Add(time.Hour) {
		setClock(t)
		dayBack := int(anchor.Sub(t).Hours() / 24)

		present := func(d *device) bool {
			first := d.firstDay
			if first == 0 {
				first = n.days + 1
			}

			return dayBack < first && dayBack >= d.lastDay
		}

		for _, d := range n.devs {
			if d.movedTo != "" && dayBack < d.movedDay {
				d.ip, d.movedTo = d.movedTo, ""
			}
		}

		if t.Hour()%6 == 0 {
			if err := readRouter(ctx, store, n.devs, present, t); err != nil {
				return nil, err
			}

			if err := curate(ctx, store, n.devs); err != nil {
				return nil, err
			}
		}

		if t.Hour()%6 == 3 {
			if err := sweep(ctx, store, n, present, t); err != nil {
				return nil, err
			}
		}

		if dayBack == n.days-2 && t.Hour() == 2 {
			if err := portScan(ctx, store, n.devs, t, false); err != nil {
				return nil, err
			}
		}

		if dayBack < 14 {
			rec.Add(netflow{}, hourOfTraffic(rng, n, local, present, t, anchor.Sub(t)))
			setClock(t.Add(time.Hour - time.Minute))

			if err := rec.Flush(ctx); err != nil {
				return nil, err
			}
		}
	}

	// Today's port scan, then a last sweep and router read a minute before
	// the anchor, so the present devices read as seen recently.
	setClock(anchor.Add(-40 * time.Minute))

	if err := portScan(ctx, store, n.devs, anchor.Add(-40*time.Minute), true); err != nil {
		return nil, err
	}

	at := anchor.Add(-time.Minute)
	setClock(at)

	present := func(d *device) bool { return d.lastDay == 0 }
	if err := readRouter(ctx, store, n.devs, present, at); err != nil {
		return nil, err
	}

	if err := sweep(ctx, store, n, present, at); err != nil {
		return nil, err
	}

	rec.Add(netflow{}, hourOfTraffic(rng, n, local, present, at, 0))

	if err := rec.Flush(ctx); err != nil {
		return nil, err
	}

	if err := n.topology(ctx, store, n.devs); err != nil {
		return nil, err
	}

	if err := curate(ctx, store, n.devs); err != nil {
		return nil, err
	}

	return rec, watch(ctx, store, n.devs)
}

type netflow struct{}

func (netflow) Name() string            { return router }
func (netflow) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

func host(ctx context.Context, ip, mac, name string) (*hosts.Host, error) {
	return hosts.BuildHost(ctx, hosts.HostInput{IP: ip, MAC: mac, Hostname: name})
}

func readRouter(ctx context.Context, store *inventory.Store, devs []*device, present func(*device) bool, at time.Time) error {
	var facts []plugin.Fact

	for _, d := range devs {
		if !present(d) {
			continue
		}

		h, err := host(ctx, d.ip, d.mac, d.name)
		if err != nil {
			return fmt.Errorf("host %s: %w", d.ip, err)
		}

		f := plugin.Fact{Host: h, Present: true, SeenAt: at}
		if d.name != "" {
			f.HostnameSource = dbtype.HostnameFromDHCPLease
		}

		facts = append(facts, f)
	}

	_, err := store.RecordFacts(ctx, router, dbtype.SourceRouter, facts)

	return err
}

func sweep(ctx context.Context, store *inventory.Store, n *sim, present func(*device) bool, at time.Time) error {
	var found []scanner.Host

	for _, d := range n.devs {
		if !present(d) || !n.swept.Contains(netip.MustParseAddr(d.ip)) {
			continue
		}

		h, err := host(ctx, d.ip, d.mac, "")
		if err != nil {
			return err
		}

		found = append(found, scanner.Host{Host: h, RTT: time.Millisecond, SeenAt: at, Services: d.services})
	}

	_, err := store.RecordSweep(ctx, sweeper, n.swept, found)

	return err
}

// portScan records the open ports; the earlier scan predates the last port
// of each device, so today's reads as having opened it.
func portScan(ctx context.Context, store *inventory.Store, devs []*device, at time.Time, today bool) error {
	var scans []scanner.PortScan

	for _, d := range devs {
		if len(d.ports) == 0 || d.lastDay > 0 {
			continue
		}

		scanned := make([]uint16, 0, 1100)
		for p := uint16(1); p <= 1024; p++ {
			scanned = append(scanned, p)
		}

		scanned = append(scanned, d.ports...)

		open := d.ports
		if !today && len(open) > 1 {
			open = open[:len(open)-1]
		}

		scans = append(scans, scanner.PortScan{Addr: netip.MustParseAddr(d.ip), Open: open, Scanned: dedupe(scanned), SeenAt: at})
	}

	_, err := store.RecordPorts(ctx, sweeper, scans)

	return err
}

func dedupe(ports []uint16) []uint16 {
	seen := map[uint16]bool{}
	out := ports[:0]

	for _, p := range ports {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	slices.Sort(out)

	return out
}

func curate(ctx context.Context, store *inventory.Store, devs []*device) error {
	all, err := store.ListDevices(ctx, inventory.DeviceFilter{})
	if err != nil {
		return err
	}

	byMAC := map[string]*device{}
	for _, d := range devs {
		byMAC[d.mac] = d
	}

	for _, got := range all {
		d := byMAC[got.MAC]
		if d == nil || d.curated || (d.label == "" && d.group == "" && d.kind == "" && d.notes == "") {
			continue
		}

		d.curated = true

		if _, err := store.UpdateCuration(ctx, got.ID, inventory.Curation{
			Label: d.label, Group: d.group, Type: d.kind, Notes: d.notes,
		}); err != nil {
			return fmt.Errorf("curate %s: %w", d.ip, err)
		}
	}

	return nil
}

func watch(ctx context.Context, store *inventory.Store, devs []*device) error {
	all, err := store.ListDevices(ctx, inventory.DeviceFilter{})
	if err != nil {
		return err
	}

	byMAC := map[string]*device{}
	for _, d := range devs {
		byMAC[d.mac] = d
	}

	for _, got := range all {
		if d := byMAC[got.MAC]; d != nil && d.watched {
			if _, err := store.Watch(ctx, got.ID, true); err != nil {
				return err
			}
		}
	}

	return nil
}

// diurnal scales traffic by the hour before the anchor rather than the hour
// of day, so the charts have the same shape whatever zone the test runs in.
func diurnal(ago time.Duration) float64 {
	h := math.Mod(24-ago.Hours(), 24)

	return 0.25 + 0.75*math.Pow(math.Sin(math.Pi*h/24), 2)
}

func hourOfTraffic(rng *rand.Rand, n *sim, local func(netip.Addr) bool, present func(*device) bool, t time.Time, ago time.Duration) []plugin.Flow {
	var flows []plugin.Flow

	end := func() time.Time { return t.Add(time.Duration(rng.IntN(55)) * time.Minute) }
	load := diurnal(ago)

	for _, d := range n.devs {
		if !present(d) {
			continue
		}

		src := netip.MustParseAddr(d.ip)

		for _, k := range d.talks {
			if k.sinceDay > 0 && int(ago.Hours()/24) >= k.sinceDay {
				continue
			}

			peer := netip.MustParseAddr(k.peer)
			down := k.kbPerHr * 1024 * load * (0.5 + rng.Float64())
			up := down * k.upRatio
			conns := 1 + rng.IntN(6)

			var nat netip.Addr
			if !local(peer) {
				nat = outside
			}

			for range conns {
				eph := uint16(40000 + rng.IntN(20000)) //nolint:gosec // bounded well inside the type.
				e := end()
				per := func(b float64) uint64 { return uint64(b / float64(conns)) }

				if k.inbound {
					flows = append(flows,
						plugin.Flow{Src: peer, Dst: src, SrcPort: eph, DstPort: k.port, Protocol: k.proto, TCPFlags: 0x1a,
							Bytes: per(down), Packets: per(down)/1200 + 2, End: e, Exporter: exporter},
						plugin.Flow{Src: src, Dst: peer, SrcPort: k.port, DstPort: eph, Protocol: k.proto, TCPFlags: 0x1a,
							Bytes: per(up), Packets: per(up)/1200 + 2, End: e, Exporter: exporter, NATSrc: nat})

					continue
				}

				flows = append(flows,
					plugin.Flow{Src: src, Dst: peer, SrcPort: eph, DstPort: k.port, Protocol: k.proto, TCPFlags: 0x1a,
						Bytes: per(up) + 60, Packets: per(up)/1200 + 2, End: e, Exporter: exporter, NATSrc: nat},
					plugin.Flow{Src: peer, Dst: src, SrcPort: k.port, DstPort: eph, Protocol: k.proto, TCPFlags: 0x1a,
						Bytes: per(down) + 60, Packets: per(down)/1200 + 2, End: e, Exporter: exporter})
			}
		}

		if rng.IntN(3) > 0 {
			flows = append(flows, plugin.Flow{Src: src, Dst: netip.MustParseAddr("224.0.0.251"), SrcPort: 5353, DstPort: 5353, Protocol: 17,
				Bytes: uint64(200 + rng.IntN(800)), Packets: uint64(2 + rng.IntN(6)), End: end(), Exporter: exporter}) //nolint:gosec // bounded well inside the type.
		}
	}

	for _, s := range probers {
		if rng.IntN(4) != 0 {
			continue
		}

		for range 1 + rng.IntN(4) {
			port := []uint16{22, 23, 80, 443, 3389, 5900, 8080, 8443}[rng.IntN(8)]
			flows = append(flows, plugin.Flow{Src: netip.MustParseAddr(s), Dst: outside, SrcPort: uint16(30000 + rng.IntN(30000)), DstPort: port, //nolint:gosec // bounded well inside the type.
				Protocol: 6, TCPFlags: 0x02, Bytes: 44, Packets: 1, End: end(), Exporter: exporter})
		}
	}

	// A few hours back, one device probed a whole segment.
	if n.scanFrom != "" && ago > 5*time.Hour && ago <= 6*time.Hour {
		from := netip.MustParseAddr(n.scanFrom)
		dst := n.scanOf.Addr()

		for range 40 {
			dst = dst.Next()
			for _, port := range []uint16{22, 80, 443, 554, 1883, 8080} {
				flows = append(flows, plugin.Flow{Src: from, Dst: dst, SrcPort: uint16(40000 + rng.IntN(20000)), DstPort: port, //nolint:gosec // bounded well inside the type.
					Protocol: 6, TCPFlags: 0x02, Bytes: 60, Packets: 1, End: end(), Exporter: exporter})
			}
		}
	}

	return flows
}

// failedScan records a run of source that failed ago before the anchor, which
// the inventory API has no way to report.
func failedScan(ctx context.Context, conn *sql.DB, source, msg string, ago time.Duration) error {
	at := anchor.Add(-ago)

	_, err := conn.ExecContext(ctx,
		`INSERT INTO scans (source_id, kind, status, error, started_at, finished_at)
		 VALUES ((SELECT id FROM sources WHERE name = ?), 'DISCOVERY', 'FAILED', ?, ?, ?)`,
		source, msg, at.Format("2006-01-02T15:04:05.000Z"), at.Add(5*time.Second).Format("2006-01-02T15:04:05.000Z"))

	return err
}

func normalTopology(ctx context.Context, store *inventory.Store, devs []*device) error {
	byName := map[string]*device{}
	for _, d := range devs {
		byName[d.name] = d
	}

	seen := func(port, name string, vlan int) plugin.Sighting {
		return plugin.Sighting{Port: port, MAC: byName[name].mac, VLAN: vlan}
	}

	wifi := func(port, name string, vlan int, ssid, band string) plugin.Sighting {
		s := seen(port, name, vlan)
		s.WiFi, s.SSID, s.Band = true, ssid, band

		return s
	}

	wired := []struct {
		port, name string
		vlan       int
	}{
		{"ether1", "nas", 10}, {"ether2", "media-server", 10}, {"ether3", "home-assistant", 10},
		{"ether4", "dns", 10}, {"ether5", "backup", 10}, {"ether6", "hypervisor", 10},
		{"ether9", "workstation", 0}, {"ether10", "tv-living-room", 0}, {"ether11", "console", 0},
		{"ether12", "camera-garage", 20}, {"ether13", "camera-porch", 20},
	}

	type client struct {
		name string
		gone bool
	}

	home := []client{{"laptop", false}, {"phone-a", false}, {"phone-b", false}, {"tablet", true}}
	smart := []client{
		{"thermostat", false}, {"doorbell", false}, {"plug-kitchen", false},
		{"speaker-kitchen", false}, {"vacuum", false}, {"light-strip", false}, {"air-purifier", true},
	}

	for _, read := range []struct {
		at     time.Time
		recent bool
	}{{anchor.Add(-4 * 24 * time.Hour), false}, {anchor.Add(-time.Minute), true}} {
		setClock(read.at)

		gw := plugin.Topology{
			Identity: "gateway", Gateway: true, Own: []string{byName["gateway"].mac}, ReadAt: read.at,
			Ports: []plugin.TopologyPort{
				{Name: "ether2", Kind: plugin.PortWired, PVID: 1, Tagged: []int{10, 20}, Running: true},
				{Name: "ether3", Kind: plugin.PortWired, PVID: 1, Tagged: []int{20}, Running: true},
				{Name: "ether8", Kind: plugin.PortWired, PVID: 1, Running: true},
			},
			Neighbours: []plugin.Neighbour{
				{Port: "ether2", MAC: byName["switch-core"].mac, Identity: "switch-core", Platform: "MikroTik", Board: "CRS326-24G-2S+"},
				{Port: "ether3", MAC: byName["ap-hallway"].mac, Identity: "ap-hallway", Platform: "MikroTik", Board: "cAP ax"},
			},
			Seen: []plugin.Sighting{seen("ether8", "printer", 0), seen("ether2", "switch-core", 0), seen("ether3", "ap-hallway", 0)},
		}

		sw := plugin.Topology{
			Identity: "switch-core", Own: []string{byName["switch-core"].mac}, ReadAt: read.at,
			Ports: []plugin.TopologyPort{{Name: "sfp1", Kind: plugin.PortWired, PVID: 1, Tagged: []int{10, 20}, Running: true}},
			Seen:  []plugin.Sighting{seen("sfp1", "gateway", 0), seen("sfp1", "ap-hallway", 0)},
		}

		ap := plugin.Topology{
			Identity: "ap-hallway", Own: []string{byName["ap-hallway"].mac}, ReadAt: read.at,
			Ports: []plugin.TopologyPort{
				{Name: "ether1", Kind: plugin.PortWired, PVID: 1, Tagged: []int{20}, Running: true},
				{Name: "wifi1", Kind: plugin.PortWiFi, PVID: 1, Untagged: []int{1}, Running: true},
				{Name: "wifi2", Kind: plugin.PortWiFi, PVID: 20, Untagged: []int{20}, Running: true},
			},
			Seen: []plugin.Sighting{seen("ether1", "gateway", 0), seen("ether1", "switch-core", 0)},
		}

		for _, w := range wired {
			gw.Seen = append(gw.Seen, seen("ether2", w.name, w.vlan))
			sw.Seen = append(sw.Seen, seen(w.port, w.name, w.vlan))
			sw.Ports = append(sw.Ports, plugin.TopologyPort{Name: w.port, Kind: plugin.PortWired, PVID: max(w.vlan, 1), Running: true})
		}

		for _, c := range home {
			if c.gone && read.recent {
				continue
			}

			gw.Seen = append(gw.Seen, seen("ether3", c.name, 0))
			ap.Seen = append(ap.Seen, wifi("wifi1", c.name, 0, "Home", "5ghz-ax"))
		}

		for _, c := range smart {
			if c.gone && read.recent {
				continue
			}

			gw.Seen = append(gw.Seen, seen("ether3", c.name, 20))
			ap.Seen = append(ap.Seen, wifi("wifi2", c.name, 20, "Things", "2ghz-n"))
		}

		for _, r := range []struct {
			name string
			t    plugin.Topology
		}{{"routeros:gateway", gw}, {"routeros:switch_core", sw}, {"routeros:ap_hallway", ap}} {
			if err := store.RecordTopology(ctx, r.name, dbtype.SourceRouter, r.t); err != nil {
				return err
			}
		}
	}

	return nil
}

// weirdTopology is a 48-port switch with long port names hanging off the
// gateway, every remaining device on one access point.
func weirdTopology(ctx context.Context, store *inventory.Store, devs []*device) error {
	at := anchor.Add(-time.Minute)
	setClock(at)

	gw := plugin.Topology{
		Identity: router, Gateway: true, Own: []string{devs[0].mac}, ReadAt: at,
		Ports: []plugin.TopologyPort{
			{Name: "sfp-sfpplus1-uplink-to-distribution", Kind: plugin.PortWired, PVID: 1, Tagged: []int{20, 30, 40, 50, 60, 70, 80, 90, 100, 4094}, Running: true},
			{Name: "ether2", Kind: plugin.PortWired, PVID: 1, Running: true},
		},
		Neighbours: []plugin.Neighbour{
			{Port: "sfp-sfpplus1-uplink-to-distribution", MAC: devs[1].mac, Identity: longHost, Platform: "MikroTik", Board: "CRS354-48G-4S+2Q+"},
		},
	}

	sw := plugin.Topology{Identity: longHost, Own: []string{devs[1].mac}, ReadAt: at}

	ap := plugin.Topology{
		Identity: "ap", Own: []string{devs[2].mac}, ReadAt: at,
		Ports: []plugin.TopologyPort{
			{Name: "ether1", Kind: plugin.PortWired, PVID: 1, Running: true},
			{Name: "wifi-5ghz-ax-guest-network-isolated", Kind: plugin.PortWiFi, PVID: 30, Untagged: []int{30}, Running: true},
		},
		Seen: []plugin.Sighting{{Port: "ether1", MAC: devs[0].mac}},
	}

	sw.Ports = append(sw.Ports, plugin.TopologyPort{Name: "uplink", Kind: plugin.PortWired, PVID: 1, Running: true})
	sw.Seen = append(sw.Seen, plugin.Sighting{Port: "uplink", MAC: devs[0].mac})
	gw.Seen = append(gw.Seen, plugin.Sighting{Port: "sfp-sfpplus1-uplink-to-distribution", MAC: devs[1].mac})

	for i, d := range devs[3:] {
		if i < 48 {
			port := fmt.Sprintf("ether%d", i+1)
			sw.Ports = append(sw.Ports, plugin.TopologyPort{Name: port, Kind: plugin.PortWired, PVID: 1, Running: true})
			sw.Seen = append(sw.Seen, plugin.Sighting{Port: port, MAC: d.mac})
			gw.Seen = append(gw.Seen, plugin.Sighting{Port: "sfp-sfpplus1-uplink-to-distribution", MAC: d.mac})

			if i == 0 {
				sw.Seen = append(sw.Seen, plugin.Sighting{Port: port, MAC: devs[2].mac})
			}

			continue
		}

		ap.Seen = append(ap.Seen, plugin.Sighting{Port: "wifi-5ghz-ax-guest-network-isolated", MAC: d.mac, VLAN: 30, WiFi: true, SSID: "Guest network 🛜 for visitors and contractors", Band: "5ghz-ax"})
		gw.Seen = append(gw.Seen, plugin.Sighting{Port: "ether2", MAC: d.mac, VLAN: 30})
	}

	for _, r := range []struct {
		name string
		t    plugin.Topology
	}{{"routeros:gateway", gw}, {"routeros:switch", sw}, {"routeros:ap", ap}} {
		if err := store.RecordTopology(ctx, r.name, dbtype.SourceRouter, r.t); err != nil {
			return err
		}
	}

	return nil
}
