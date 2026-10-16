// Package topology builds the tree of the network from what each router,
// switch and access point says is connected to it. The internet is above the
// router. Each switch and access point is on the port from which it hangs.
// Each device is on the port or Wi-Fi network where a source last saw it.
//
// No single device knows the tree. A router learns every address behind a
// trunk on that one port. The device can be on the switch at the other end,
// or on an access point behind that switch. The tables of each device tell on
// which side of it everything else is. The tree comes from those tables.
package topology

import (
	"slices"
	"time"
)

// Source is one router, switch or access point, as its last read described it.
type Source struct {
	ID   int64
	Name string

	// Identity is the name that the device gives itself.
	Identity string

	// Gateway marks the device that routes the network. The tree hangs from
	// it, below the internet.
	Gateway bool

	// Own is the hardware addresses of the device.
	Own []string

	ReadAt     time.Time
	Ports      []Port
	Seen       []Sighting
	Neighbours []Neighbour
}

// PortKind is the kind of interface of a port.
type PortKind string

// The kinds a source reports.
const (
	PortWired   PortKind = "wired"
	PortWiFi    PortKind = "wifi"
	PortVirtual PortKind = "virtual"
)

// Port is one interface of a source, with the VLANs that it carries.
type Port struct {
	Name     string
	Kind     PortKind
	PVID     int
	Tagged   []int
	Untagged []int

	// Speed is the link of the port.
	Speed Speed
}

// Speed is how fast a wired link runs, as the latest read found it. Rate is
// the rate at which it came up. Capable is the fastest rate that both ends
// offer. Both are in bits per second, and zero is unknown.
type Speed struct {
	Rate       int64
	Capable    int64
	FullDuplex bool
}

// Slow reports whether the link came up below the rate that both ends can
// run at.
func (s Speed) Slow() bool { return s.Rate > 0 && s.Rate < s.Capable }

// Radio is how the connection of a Wi-Fi client runs, as the latest read found
// it. Down is the rate at which the radio sends to the client. Up is the rate
// at which it receives from the client. Both are in bits per second. Signal is
// the signal of the client at the radio, in dBm. Zero is unknown in each.
type Radio struct {
	Down   int64
	Up     int64
	Signal int
}

// Sighting is one hardware address that a source learned on one of its
// ports.
type Sighting struct {
	Port string
	MAC  string

	// VLAN is zero when the source did not tell.
	VLAN int

	WiFi bool
	SSID string
	Band string

	// Radio is the connection of a Wi-Fi client.
	Radio Radio

	// LastSeen is the last read that listed the address. A sighting from the
	// latest read of the source is current. An older sighting is where a
	// source last saw the address.
	LastSeen time.Time
}

// Neighbour is a device that announced itself on a port of a source.
type Neighbour struct {
	Port     string
	MAC      string
	Identity string
	Board    string
}

// Device is an inventory device that the tree can place by its hardware
// address.
type Device struct {
	ID     int64
	MAC    string
	Name   string
	Class  string
	Online bool

	// VLAN is the VLAN of the network where the device has an address. It is
	// zero when unknown. The tree uses it when no source said in which VLAN
	// it saw the device.
	VLAN int
}

// NodeKind is how the tree knows that a switching device is there.
type NodeKind string

// The node kinds:
//   - a node that Jocasta reads
//   - a node that only announced itself to a device that Jocasta reads
//   - a node inferred from several devices that share one port
//   - a hypervisor on a port that it shares with its virtual machines
const (
	NodeRead    NodeKind = "read"
	NodeSeen    NodeKind = "seen"
	NodeUnnamed NodeKind = "unnamed"
	NodeHost    NodeKind = "host"
)

// Tree is the whole network, from the router down.
type Tree struct {
	// Root is the router. It is nil when no source was read.
	Root *Node

	// Unplaced is the online devices that no source saw on a port, in name
	// order.
	Unplaced []*Leaf

	// VLANs is every VLAN of a placed device, in order.
	VLANs []int

	nodeByDevice map[int64]*Node
	leafByDevice map[int64]*Leaf
}

// Node is a router, switch, access point or hypervisor.
type Node struct {
	Kind NodeKind

	// Key identifies the node across reads. A page uses it to keep its
	// selection through a refresh.
	Key string

	// Name is the identity of the device, or its inventory name. It is empty
	// for an unnamed switch.
	Name  string
	Board string

	// DeviceID is the inventory device of this node. It is zero when there
	// is none.
	DeviceID int64
	Online   bool

	// WiFi marks an unnamed node whose devices are all on Wi-Fi. It is an
	// access point that nothing announced.
	WiFi bool

	// VMs marks an unnamed node whose devices are mostly virtual machines. It
	// is a hypervisor that the inventory does not know as a hypervisor.
	VMs bool

	// Uplink is how the node hangs from Parent. It is nil on the root.
	Uplink *Link
	Parent *Node

	// Children are the switches and access points below this node, in port
	// order.
	Children []*Node

	// Groups are the devices on this node. Wired devices come first, in VLAN
	// order. Then there is one group for each Wi-Fi network.
	Groups []*Group
}

// Link is how a node hangs from its parent.
type Link struct {
	// ParentPort is the port of the parent where the node is. Port is the
	// port of the node towards the parent. Each is empty when unknown.
	ParentPort string
	Port       string

	// VLANs is what the port of the parent carries. Trunk marks a port that
	// carries tagged VLANs.
	VLANs []int
	Trunk bool

	// Speed is the link from which the node hangs.
	Speed Speed
}

// Group is the devices on a node in one VLAN: the wired devices, or the
// devices on one Wi-Fi network. Each device tells its port.
type Group struct {
	WiFi bool
	SSID string
	VLAN int

	Devices []*Leaf
}

// Leaf is one device placed in the tree.
type Leaf struct {
	DeviceID int64
	MAC      string
	Name     string
	Class    string
	Online   bool

	// VLANs is every VLAN in which a source saw the device. VLAN is the VLAN
	// of its group.
	VLANs []int
	VLAN  int

	WiFi bool
	SSID string
	Band string

	// Port is the port of Owner where the device is. It is empty when
	// unknown.
	Port  string
	Owner *Node

	// Current reports whether the source of Owner saw the device within the
	// window of its latest read. A device placed from an older read is where a
	// source last saw it.
	Current bool

	// Speed is the link of Port. It is set when the device is alone on a port
	// of a read source. Radio is the connection of a Wi-Fi client. Both are
	// zero for a device behind a node, because the link belongs to the node.
	// Both are also zero for a device placed from an older read.
	Speed Speed
	Radio Radio
}

// Node returns the node that is device id. It returns false when the device is
// not a node in the tree.
func (t *Tree) Node(id int64) (*Node, bool) {
	n, ok := t.nodeByDevice[id]

	return n, ok
}

// Leaf returns where device id is placed. It returns false when it is not
// placed.
func (t *Tree) Leaf(id int64) (*Leaf, bool) {
	l, ok := t.leafByDevice[id]

	return l, ok
}

// Path returns the nodes from the root down to n, with n.
func Path(n *Node) []*Node {
	var out []*Node

	for ; n != nil; n = n.Parent {
		out = append(out, n)
	}

	slices.Reverse(out)

	return out
}
