package topomap

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/topology"
	"github.com/pushkar-anand/jocasta/internal/web/netmap"
)

// network builds a router with a switch on a trunk and an access point on
// another, each with devices, plus a device on the router itself. Hardware
// addresses are RFC 7042 documentation values.
func network(t *testing.T, perSwitch int) *topology.Tree {
	t.Helper()

	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	mac := func(n int) string { return fmt.Sprintf("00:00:5e:00:53:%02x", n) }

	router := topology.Source{
		ID: 1, Name: "routeros:gateway", Identity: "router", Gateway: true, ReadAt: now,
		Own: []string{mac(0xa0)},
		Ports: []topology.Port{
			{Name: "ether2", Kind: topology.PortWired, Tagged: []int{10, 20}},
			{Name: "ether7", Kind: topology.PortWired, PVID: 10, Untagged: []int{10}},
			{Name: "sfp1", Kind: topology.PortWired, Tagged: []int{10, 30}},
		},
		Neighbours: []topology.Neighbour{
			{Port: "ether2", MAC: mac(0xc0), Identity: "ap-hall"},
			{Port: "sfp1", MAC: mac(0xb0), Identity: "switch-a"},
		},
	}

	var devices []topology.Device

	add := func(m int, port string, vlan int, ssid string) {
		router.Seen = append(router.Seen, topology.Sighting{
			Port: port, MAC: mac(m), VLAN: vlan, WiFi: ssid != "", SSID: ssid, LastSeen: now,
		})
		devices = append(devices, topology.Device{ID: int64(m), MAC: mac(m), Name: fmt.Sprintf("device-%d", m), Online: true})
	}

	add(1, "ether7", 10, "")

	for i := range perSwitch {
		add(10+i, "sfp1", 10+20*(i%2), "")
	}

	add(80, "ether2", 10, "home")
	add(81, "ether2", 20, "iot")
	add(82, "ether2", 20, "iot")

	return topology.Build([]topology.Source{router}, devices)
}

func TestPlaceWithNothingRead(t *testing.T) {
	t.Parallel()

	assert.Nil(t, Place(nil))
	assert.Nil(t, Place(topology.Build(nil, nil)))
}

func TestPlaceIsStable(t *testing.T) {
	t.Parallel()

	first := Place(network(t, 6))

	for range 10 {
		assert.Equal(t, first, Place(network(t, 6)))
	}
}

// Nothing overlaps: no two boxes or groups share any of the canvas.
func TestPlaceKeepsBoxesApart(t *testing.T) {
	t.Parallel()

	l := Place(network(t, 12))
	require.NotNil(t, l)

	type rect struct {
		name       string
		x, y, w, h float64
	}

	var rs []rect
	for _, b := range l.Boxes {
		rs = append(rs, rect{b.Label, b.X, b.Y, b.W, b.H})
	}

	for _, g := range l.Groups {
		rs = append(rs, rect{g.Title, g.X, g.Y, g.W, g.H})
	}

	for i, a := range rs {
		assert.GreaterOrEqual(t, a.x, margin-0.01, a.name)
		assert.LessOrEqual(t, a.x+a.w, l.Width-margin+0.01, a.name)
		assert.LessOrEqual(t, a.y+a.h, l.Height, a.name)

		for _, b := range rs[i+1:] {
			apart := a.x+a.w <= b.x || b.x+b.w <= a.x || a.y+a.h <= b.y || b.y+b.h <= a.y
			assert.True(t, apart, "%s overlaps %s", a.name, b.name)
		}
	}
}

// The internet is above the router, and everything hangs below its parent.
func TestPlaceReadsTopDown(t *testing.T) {
	t.Parallel()

	l := Place(network(t, 2))
	require.NotNil(t, l)

	root := l.Boxes[0]
	assert.Equal(t, "router", root.Label)
	assert.Less(t, l.Internet.Y, root.Y)

	for _, b := range l.Boxes[1:] {
		assert.Greater(t, b.Y, root.Y+root.H, b.Label)
	}

	for _, g := range l.Groups {
		assert.Greater(t, g.Y, root.Y+root.H, g.Title)
	}
}

// Every box but the router, and every group, has a line to it, and the
// internet one to the router.
func TestPlaceDrawsEveryBranch(t *testing.T) {
	t.Parallel()

	l := Place(network(t, 2))
	require.NotNil(t, l)

	assert.Len(t, l.Edges, len(l.Boxes)+len(l.Groups))

	kinds := map[string]int{}
	labels := map[string]bool{}

	for _, e := range l.Edges {
		kinds[e.Kind]++
		labels[e.Label] = true
	}

	assert.Equal(t, 2, kinds["wifi"], "one line per Wi-Fi network")
	assert.True(t, labels["sfp1 · trunk"])
	assert.True(t, labels["ether2 · trunk"])
}

// Groups are titled by port or Wi-Fi network and VLAN, and devices take their
// VLAN's colour.
func TestPlaceTitlesGroups(t *testing.T) {
	t.Parallel()

	l := Place(network(t, 2))
	require.NotNil(t, l)

	titles := make([]string, len(l.Groups))
	for i, g := range l.Groups {
		titles[i] = g.Title
	}

	// In port order: the access point on ether2 first.
	assert.Equal(t, []string{"home · VLAN 10", "iot · VLAN 20", "ether7 · VLAN 10", "VLAN 10", "VLAN 30"}, titles)

	for _, g := range l.Groups {
		for _, c := range g.Chips {
			assert.Equal(t, g.Tone, c.Tone, c.Label)
		}
	}
}

// Selecting a device lights every box and line on its path: each carries the
// keys below it.
func TestPlaceMarksThePathToEachDevice(t *testing.T) {
	t.Parallel()

	l := Place(network(t, 2))
	require.NotNil(t, l)

	key := netmap.DeviceKey(81)

	var on []string

	for _, b := range l.Boxes {
		if strings.Contains(" "+b.On+" ", " "+key+" ") {
			on = append(on, b.Label)
		}
	}

	assert.Equal(t, []string{"router", "ap-hall"}, on)
}

func TestShortenCutsLongNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "short", shorten("short"))
	assert.Equal(t, maxLabel, len([]rune(shorten(strings.Repeat("x", 40)))))
}

func TestNodeNameSaysWhatAnUnnamedNodeIs(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "vm-host", nodeName(&topology.Node{Kind: topology.NodeHost, Name: "vm-host"}))
	assert.Equal(t, "Access point", nodeName(&topology.Node{Kind: topology.NodeUnnamed, WiFi: true}))
	assert.Equal(t, "Hypervisor", nodeName(&topology.Node{Kind: topology.NodeUnnamed, VMs: true}))
	assert.Equal(t, "Switch", nodeName(&topology.Node{Kind: topology.NodeUnnamed}))
}
