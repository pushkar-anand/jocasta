package topology

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Hardware addresses are RFC 7042 documentation values: 00:00:5e:00:53:xx.
func mac(n int) string { return fmt.Sprintf("00:00:5e:00:53:%02x", n) }

var (
	now     = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	earlier = now.Add(-time.Hour)
)

// Infrastructure addresses.
var (
	routerMAC = mac(0xa0)
	switchMAC = mac(0xb0)
	apMAC     = mac(0xc0)
)

func seen(port string, m, vlan int) Sighting {
	return Sighting{Port: port, MAC: mac(m), VLAN: vlan, LastSeen: now}
}

func wifi(port string, m, vlan int, ssid string) Sighting {
	s := seen(port, m, vlan)
	s.WiFi, s.SSID, s.Band = true, ssid, "5ghz-ax"

	return s
}

func old(s Sighting) Sighting {
	s.LastSeen = earlier

	return s
}

func dev(m int, name string, online bool) Device {
	return Device{ID: int64(m), MAC: mac(m), Name: name, Online: online}
}

// render draws the tree as indented text, which keeps a whole-tree assertion
// readable.
func render(t *Tree) string {
	var sb strings.Builder

	var walk func(n *Node, depth int)

	walk = func(n *Node, depth int) {
		indent := strings.Repeat("  ", depth)

		up := ""
		if n.Uplink != nil {
			up = fmt.Sprintf(" via %q", n.Uplink.ParentPort)
			if n.Uplink.Trunk {
				up += " trunk"
			}
		}

		fmt.Fprintf(&sb, "%s%s %s%s\n", indent, n.Kind, n.Name, up)

		for _, g := range n.Groups {
			names := make([]string, len(g.Devices))
			for i, d := range g.Devices {
				names[i] = d.Name
				if d.Port != "" && !g.WiFi {
					names[i] += "@" + d.Port
				}
			}

			where := "wired"
			if g.WiFi {
				where = "wifi " + g.SSID
			}

			fmt.Fprintf(&sb, "%s  [%s vlan %d] %s\n", indent, where, g.VLAN, strings.Join(names, ", "))
		}

		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}

	if t.Root != nil {
		walk(t.Root, 0)
	}

	return sb.String()
}

// Only the router is read. The switch and access point are known from what
// they announced, and hold the devices behind their ports.
func TestBuildFromTheRouterAlone(t *testing.T) {
	t.Parallel()

	router := Source{
		ID: 1, Name: "routeros:gateway", Identity: "router", Gateway: true, ReadAt: now,
		Own: []string{routerMAC},
		Ports: []Port{
			{Name: "ether2", Kind: PortWired, PVID: 99, Tagged: []int{10, 20}, Untagged: []int{99}},
			{Name: "ether7", Kind: PortWired, PVID: 10, Untagged: []int{10}},
			{Name: "sfp1", Kind: PortWired, PVID: 1, Tagged: []int{10, 20, 99}},
		},
		Seen: []Sighting{
			seen("ether2", 0xc0, 99),
			wifi("ether2", 1, 10, "home"),
			wifi("ether2", 2, 20, "iot"),
			seen("ether7", 3, 10),
			seen("sfp1", 0xb0, 99),
			seen("sfp1", 4, 10),
			seen("sfp1", 5, 10),
		},
		Neighbours: []Neighbour{
			{Port: "ether2", MAC: apMAC, Identity: "ap-hall", Board: "cAP ax"},
			{Port: "sfp1", MAC: switchMAC, Identity: "switch-a", Board: "CRS326"},
		},
	}

	tree := Build([]Source{router}, []Device{
		dev(1, "phone", true), dev(2, "plug", true), dev(3, "pi", true),
		dev(4, "desktop", true), dev(5, "nas", false),
		dev(0xa0, "router-device", true), dev(0xb0, "switch-device", true), dev(0xc0, "ap-device", true),
		dev(9, "laptop", true),
	})

	assert.Equal(t, `read router
  [wired vlan 10] pi@ether7
  seen ap-hall via "ether2" trunk
    [wifi home vlan 10] phone
    [wifi iot vlan 20] plug
  seen switch-a via "sfp1" trunk
    [wired vlan 10] desktop, nas
`, render(tree))

	assert.Equal(t, []int{10, 20}, tree.VLANs)

	// The switch and access point are their inventory devices, not leaves.
	sw, ok := tree.Node(0xb0)
	require.True(t, ok)
	assert.Equal(t, "CRS326", sw.Board)

	_, ok = tree.Leaf(0xb0)
	assert.False(t, ok)

	rt, ok := tree.Node(0xa0)
	require.True(t, ok)
	assert.Same(t, tree.Root, rt)

	// An online device nothing has seen on a port is listed apart.
	require.Len(t, tree.Unplaced, 1)
	assert.Equal(t, "laptop", tree.Unplaced[0].Name)
}

// The router, the switch and the access point are all read, the access point
// hanging from the switch. The router learns everything behind its trunk on
// one port; the switch's and access point's tables say where each device is.
func TestBuildFromEveryDevice(t *testing.T) {
	t.Parallel()

	router := Source{
		ID: 1, Name: "routeros:gateway", Identity: "router", Gateway: true, ReadAt: now,
		Own:   []string{routerMAC},
		Ports: []Port{{Name: "sfp1", Kind: PortWired, Tagged: []int{10, 20}}},
		Seen: []Sighting{
			seen("sfp1", 0xb0, 99), seen("sfp1", 0xc0, 99),
			seen("sfp1", 1, 10), seen("sfp1", 2, 20), seen("sfp1", 4, 10),
		},
		Neighbours: []Neighbour{
			{Port: "sfp1", MAC: switchMAC, Identity: "switch-a"},
			{Port: "sfp1", MAC: apMAC, Identity: "ap-hall"},
		},
	}

	sw := Source{
		ID: 2, Name: "routeros:switch_a", Identity: "switch-a", ReadAt: now,
		Own: []string{switchMAC},
		Ports: []Port{
			{Name: "sfp1", Kind: PortWired, Tagged: []int{10, 20}},
			{Name: "ether4", Kind: PortWired, PVID: 10, Untagged: []int{10}},
			{Name: "ether8", Kind: PortWired, Tagged: []int{10, 20}},
		},
		Seen: []Sighting{
			seen("sfp1", 0xa0, 99),
			seen("ether4", 4, 10),
			seen("ether8", 0xc0, 99), seen("ether8", 1, 10), seen("ether8", 2, 20),
		},
		Neighbours: []Neighbour{
			{Port: "sfp1", MAC: routerMAC, Identity: "router"},
			{Port: "ether8", MAC: apMAC, Identity: "ap-hall"},
		},
	}

	ap := Source{
		ID: 3, Name: "routeros:ap_hall", Identity: "ap-hall", ReadAt: now,
		Own:   []string{apMAC},
		Ports: []Port{{Name: "ether1", Kind: PortWired}, {Name: "wifi1", Kind: PortWiFi}, {Name: "wifi2", Kind: PortWiFi}},
		Seen: []Sighting{
			seen("ether1", 0xa0, 99), seen("ether1", 0xb0, 99),
			wifi("wifi1", 1, 10, "home"), wifi("wifi2", 2, 20, "iot"),
		},
	}

	tree := Build([]Source{sw, ap, router}, []Device{
		dev(1, "phone", true), dev(2, "plug", true), dev(4, "desktop", true),
	})

	assert.Equal(t, `read router
  read switch-a via "sfp1" trunk
    [wired vlan 10] desktop@ether4
    read ap-hall via "ether8" trunk
      [wifi home vlan 10] phone
      [wifi iot vlan 20] plug
`, render(tree))

	phone, ok := tree.Leaf(1)
	require.True(t, ok)
	assert.Equal(t, "wifi1", phone.Port)
	assert.True(t, phone.Current)

	hops := Path(phone.Owner)
	require.Len(t, hops, 3)
	assert.Equal(t, "router", hops[0].Name)
	assert.Equal(t, "switch-a", hops[1].Name)
	assert.Equal(t, "ap-hall", hops[2].Name)
	assert.Equal(t, "sfp1", hops[1].Uplink.ParentPort)
	assert.Equal(t, "sfp1", hops[1].Uplink.Port)
	assert.Equal(t, "ether8", hops[2].Uplink.ParentPort)
	assert.Equal(t, "ether1", hops[2].Uplink.Port)
}

// A device that moved from a switch port to Wi-Fi is placed where the latest
// reads saw it. One that has gone quiet stays where it was last seen.
func TestBuildPrefersTheLatestRead(t *testing.T) {
	t.Parallel()

	router := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{{Name: "ether2", Kind: PortWired}, {Name: "ether3", Kind: PortWired}, {Name: "wifi1", Kind: PortWiFi}},
		Seen: []Sighting{
			old(seen("ether2", 1, 10)),
			wifi("wifi1", 1, 10, "home"),
			old(seen("ether3", 2, 10)),
		},
	}

	tree := Build([]Source{router}, []Device{dev(1, "laptop", true), dev(2, "printer", false)})

	laptop, ok := tree.Leaf(1)
	require.True(t, ok)
	assert.True(t, laptop.WiFi)
	assert.Equal(t, "wifi1", laptop.Port)
	assert.True(t, laptop.Current)

	printer, ok := tree.Leaf(2)
	require.True(t, ok)
	assert.Equal(t, "ether3", printer.Port)
	assert.False(t, printer.Current)
}

// Several devices on one wired port of a source, with nothing announcing
// itself there, are behind something the tree does not read.
func TestBuildInfersAnUnnamedSwitch(t *testing.T) {
	t.Parallel()

	router := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{{Name: "ether5", Kind: PortWired, PVID: 10}, {Name: "ether6", Kind: PortWired, PVID: 10}},
		Seen: []Sighting{
			seen("ether5", 1, 0), seen("ether5", 2, 0),
			seen("ether6", 3, 0),
		},
	}

	tree := Build([]Source{router}, []Device{dev(1, "tv", true), dev(2, "console", true), dev(3, "desk", true)})

	assert.Equal(t, `read router
  [wired vlan 10] desk@ether6
  unnamed  via "ether5"
    [wired vlan 10] console, tv
`, render(tree))
}

// A router managing access points it does not read, with no neighbour
// announcing them, still shows its Wi-Fi clients behind an access point.
func TestBuildMarksAnUnnamedAccessPoint(t *testing.T) {
	t.Parallel()

	router := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{{Name: "ether2", Kind: PortWired}},
		Seen:  []Sighting{wifi("ether2", 1, 10, "home"), wifi("ether2", 2, 10, "home")},
	}

	tree := Build([]Source{router}, []Device{dev(1, "phone", true), dev(2, "tablet", true)})

	require.Len(t, tree.Root.Children, 1)
	ap := tree.Root.Children[0]
	assert.Equal(t, NodeUnnamed, ap.Kind)
	assert.True(t, ap.WiFi)
}

// Two read switches behind one port of the router, neither seeing the other
// below it, hang from something between them the tree does not read.
func TestBuildHangsSiblingsFromWhatIsBetween(t *testing.T) {
	t.Parallel()

	router := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{{Name: "ether2", Kind: PortWired}},
		Seen:  []Sighting{seen("ether2", 0xb0, 0), seen("ether2", 0xb1, 0)},
	}

	left := Source{
		ID: 2, Name: "b", Identity: "switch-left", ReadAt: now, Own: []string{mac(0xb0)},
		Seen: []Sighting{seen("ether1", 0xa0, 0), seen("ether1", 0xb1, 0)},
	}

	right := Source{
		ID: 3, Name: "c", Identity: "switch-right", ReadAt: now, Own: []string{mac(0xb1)},
		Seen: []Sighting{seen("ether1", 0xa0, 0), seen("ether1", 0xb0, 0)},
	}

	tree := Build([]Source{router, left, right}, nil)

	assert.Equal(t, `read router
  unnamed  via "ether2"
    read switch-left via ""
    read switch-right via ""
`, render(tree))
}

func TestBuildWithNothingRead(t *testing.T) {
	t.Parallel()

	tree := Build(nil, []Device{dev(1, "phone", true), dev(2, "old", false)})

	assert.Nil(t, tree.Root)
	require.Len(t, tree.Unplaced, 1)
	assert.Equal(t, "phone", tree.Unplaced[0].Name)
}

// The same input builds the same tree, so a page that refreshes does not
// shuffle.
func TestBuildIsStable(t *testing.T) {
	t.Parallel()

	src := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{{Name: "ether10", Kind: PortWired}, {Name: "ether2", Kind: PortWired}},
		Seen:  []Sighting{seen("ether10", 1, 0), seen("ether2", 2, 0)},
	}
	devices := []Device{dev(1, "b", true), dev(2, "a", true)}

	first := render(Build([]Source{src}, devices))

	for range 20 {
		assert.Equal(t, first, render(Build([]Source{src}, devices)))
	}

	assert.Equal(t, `read router
  [wired vlan 0] a@ether2, b@ether10
`, first)
}

func TestComparePortsOrdersPortNumbers(t *testing.T) {
	t.Parallel()

	assert.Negative(t, ComparePorts("ether2", "ether10"))
	assert.Positive(t, ComparePorts("ether10", "ether9"))
	assert.Negative(t, ComparePorts("ether1", "sfp1"))
	assert.Zero(t, ComparePorts("wifi1", "wifi1"))
	assert.Negative(t, ComparePorts("", "ether1"))
}

// A switch learns every Wi-Fi client of an access point beside it on its
// uplink, and the access point learns the switch's wired devices on its own.
// Neither is where those devices are: each is placed where it plugs in or
// associates, however recently a source heard it through its uplink.
func TestBuildIgnoresWhatAnUplinkLearns(t *testing.T) {
	t.Parallel()

	router := Source{
		ID: 1, Name: "routeros:gateway", Identity: "router", Gateway: true, ReadAt: earlier,
		Own: []string{routerMAC},
		Ports: []Port{
			{Name: "ether2", Kind: PortWired, Tagged: []int{10, 20}},
			{Name: "sfp1", Kind: PortWired, Tagged: []int{10, 20}},
		},
		Seen: []Sighting{
			old(seen("ether2", 0xc0, 0)), old(seen("ether2", 1, 10)), old(seen("ether2", 2, 20)),
			old(seen("sfp1", 0xb0, 0)), old(seen("sfp1", 3, 10)), old(seen("sfp1", 4, 10)),
		},
		Neighbours: []Neighbour{
			{Port: "ether2", MAC: apMAC, Identity: "ap-hall"},
			{Port: "sfp1", MAC: switchMAC, Identity: "switch-a"},
		},
	}

	sw := Source{
		ID: 2, Name: "routeros:switch_a", Identity: "switch-a", ReadAt: now,
		Own:   []string{switchMAC},
		Ports: []Port{{Name: "sfp1", Kind: PortWired, Tagged: []int{10, 20}}, {Name: "ether3", Kind: PortWired, PVID: 10}, {Name: "ether4", Kind: PortWired, PVID: 10}},
		Seen: []Sighting{
			seen("sfp1", 0xa0, 0), seen("sfp1", 0xc0, 0), seen("sfp1", 1, 10), seen("sfp1", 2, 20),
			seen("ether3", 3, 10), seen("ether4", 4, 10),
		},
		Neighbours: []Neighbour{{Port: "sfp1", MAC: routerMAC, Identity: "router"}},
	}

	ap := Source{
		ID: 3, Name: "routeros:ap_hall", Identity: "ap-hall", ReadAt: earlier,
		Own:   []string{apMAC},
		Ports: []Port{{Name: "ether1", Kind: PortWired, Tagged: []int{10, 20}}, {Name: "wifi1", Kind: PortWiFi}, {Name: "wifi2", Kind: PortWiFi}},
		Seen: []Sighting{
			old(seen("ether1", 0xa0, 0)), old(seen("ether1", 3, 10)), old(seen("ether1", 4, 10)),
			old(wifi("wifi1", 1, 10, "home")), old(wifi("wifi2", 2, 20, "iot")),
		},
		Neighbours: []Neighbour{{Port: "ether1", MAC: routerMAC, Identity: "router"}},
	}

	tree := Build([]Source{router, sw, ap}, []Device{
		dev(1, "phone", true), dev(2, "plug", true), dev(3, "server", true), dev(4, "nas", true),
	})

	assert.Equal(t, `read router
  read ap-hall via "ether2" trunk
    [wifi home vlan 10] phone
    [wifi iot vlan 20] plug
  read switch-a via "sfp1" trunk
    [wired vlan 10] server@ether3, nas@ether4
`, render(tree))
}

// A hypervisor sharing a port with its virtual machines is the node on that
// port, whether the classifier or its owner said so.
func TestBuildHangsVirtualMachinesFromTheirHost(t *testing.T) {
	t.Parallel()

	router := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{{Name: "ether7", Kind: PortWired, PVID: 10}},
		Seen:  []Sighting{seen("ether7", 1, 10), seen("ether7", 2, 10), seen("ether7", 3, 10)},
	}

	host := dev(1, "vm-host", true)
	host.Class = "hypervisor"

	tree := Build([]Source{router}, []Device{host, dev(2, "web", true), dev(3, "db", true)})

	assert.Equal(t, `read router
  host vm-host via "ether7"
    [wired vlan 10] db, web
`, render(tree))

	n, ok := tree.Node(1)
	require.True(t, ok)
	assert.Equal(t, NodeHost, n.Kind)

	_, isLeaf := tree.Leaf(1)
	assert.False(t, isLeaf)
	assert.Empty(t, tree.Unplaced)
}

// With two hypervisors on one port, neither is plainly the host.
func TestBuildLeavesTwoHypervisorsBehindASwitch(t *testing.T) {
	t.Parallel()

	router := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{{Name: "ether7", Kind: PortWired, PVID: 10}},
		Seen:  []Sighting{seen("ether7", 1, 10), seen("ether7", 2, 10)},
	}

	a, c := dev(1, "host-a", true), dev(2, "host-b", true)
	a.Class, c.Class = "hypervisor", "hypervisor"

	tree := Build([]Source{router}, []Device{a, c})

	require.Len(t, tree.Root.Children, 1)
	assert.Equal(t, NodeUnnamed, tree.Root.Children[0].Kind)
	assert.False(t, tree.Root.Children[0].VMs)
}

// Devices mostly carrying virtual machine addresses behind one port, with no
// hypervisor known, are behind a virtual machine host.
func TestBuildMarksAVirtualMachineHost(t *testing.T) {
	t.Parallel()

	// VirtualBox and QEMU address blocks; the rest of each address is
	// arbitrary.
	vbox, qemu, plain := "08:00:27:00:00:01", "52:54:00:00:00:02", mac(3)

	router := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{{Name: "ether7", Kind: PortWired, PVID: 10}},
		Seen: []Sighting{
			{Port: "ether7", MAC: vbox, LastSeen: now},
			{Port: "ether7", MAC: qemu, LastSeen: now},
			{Port: "ether7", MAC: plain, LastSeen: now},
		},
	}

	tree := Build([]Source{router}, []Device{
		{ID: 1, MAC: vbox, Name: "vm-a", Online: true},
		{ID: 2, MAC: qemu, Name: "vm-b", Online: true},
		{ID: 3, MAC: plain, Name: "desktop", Online: true},
	})

	require.Len(t, tree.Root.Children, 1)
	n := tree.Root.Children[0]
	assert.Equal(t, NodeUnnamed, n.Kind)
	assert.True(t, n.VMs)
}

// A device alone on a port has that port's link. A hypervisor on a port has
// the link, and its virtual machines, behind it, have none. So do the devices
// behind an unnamed switch, whose link is the switch's.
func TestBuildGivesTheLinkToWhatIsPluggedIn(t *testing.T) {
	t.Parallel()

	gigabit := Speed{Rate: 1_000_000_000, Capable: 1_000_000_000, FullDuplex: true}
	slow := Speed{Rate: 100_000_000, Capable: 1_000_000_000, FullDuplex: true}

	router := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{
			{Name: "ether2", Kind: PortWired, PVID: 10, Speed: slow},
			{Name: "ether3", Kind: PortWired, PVID: 10, Speed: gigabit},
			{Name: "ether7", Kind: PortWired, PVID: 10, Speed: gigabit},
		},
		Seen: []Sighting{
			seen("ether2", 1, 10),
			seen("ether3", 2, 10), seen("ether3", 3, 10),
			seen("ether7", 4, 10), seen("ether7", 5, 10),
		},
	}

	host := dev(4, "vm-host", true)
	host.Class = "hypervisor"

	tree := Build([]Source{router}, []Device{
		dev(1, "desktop", true), dev(2, "tv", true), dev(3, "console", true), host, dev(5, "web", true),
	})

	desktop, ok := tree.Leaf(1)
	require.True(t, ok)
	assert.Equal(t, slow, desktop.Speed)
	assert.True(t, desktop.Speed.Slow())
	assert.False(t, gigabit.Slow())

	tv, ok := tree.Leaf(2)
	require.True(t, ok)
	assert.Zero(t, tv.Speed, "behind an unnamed switch")
	assert.Equal(t, gigabit, tv.Owner.Uplink.Speed, "the switch has the link")

	n, ok := tree.Node(4)
	require.True(t, ok)
	assert.Equal(t, gigabit, n.Uplink.Speed)

	web, ok := tree.Leaf(5)
	require.True(t, ok)
	assert.Zero(t, web.Speed, "a virtual machine is behind its host")
}

// A Wi-Fi client has its radio's rates. One placed from an older read has
// none, and neither does a wired device placed from one.
func TestBuildGivesAWiFiClientItsRates(t *testing.T) {
	t.Parallel()

	phone := wifi("wifi1", 1, 20, "home")
	phone.Radio = Radio{Down: 866_600_000, Up: 650_000_000, Signal: -54}

	gone := wifi("wifi1", 2, 20, "home")
	gone.Radio = Radio{Down: 54_000_000, Up: 54_000_000, Signal: -80}

	router := Source{
		ID: 1, Name: "a", Identity: "router", Gateway: true, ReadAt: now, Own: []string{routerMAC},
		Ports: []Port{
			{Name: "wifi1", Kind: PortWiFi, PVID: 20},
			{Name: "ether2", Kind: PortWired, PVID: 10, Speed: Speed{Rate: 1_000_000_000}},
		},
		Seen: []Sighting{phone, old(gone), old(seen("ether2", 3, 10))},
	}

	tree := Build([]Source{router}, []Device{dev(1, "phone", true), dev(2, "tablet", false), dev(3, "nas", false)})

	l, ok := tree.Leaf(1)
	require.True(t, ok)
	assert.Equal(t, Radio{Down: 866_600_000, Up: 650_000_000, Signal: -54}, l.Radio)
	assert.Zero(t, l.Speed)

	for _, id := range []int64{2, 3} {
		l, ok := tree.Leaf(id)
		require.True(t, ok)
		assert.False(t, l.Current)
		assert.Zero(t, l.Speed, l.Name)
		assert.Zero(t, l.Radio, l.Name)
	}
}
