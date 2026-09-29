// Package topology assembles the network's tree from what each router, switch
// and access point says is plugged into it: the internet above the router,
// each switch and access point on the port it hangs from, and each device on
// the port or Wi-Fi network it was last seen on.
//
// No single device knows the tree. A router learns every address behind a
// trunk on that one port, whether the device is on the switch at the other
// end or on an access point behind that switch. Each device's own tables say
// which side of it everything else is on, and the tree follows from those.
package topology

import (
	"slices"
	"time"
)

// Source is one router, switch or access point as its last read described it.
type Source struct {
	ID   int64
	Name string

	// Identity is the device's own name for itself.
	Identity string

	// Gateway marks the device that routes the network. The tree hangs from
	// it, below the internet.
	Gateway bool

	// Own is the device's own hardware addresses.
	Own []string

	ReadAt     time.Time
	Ports      []Port
	Seen       []Sighting
	Neighbours []Neighbour
}

// PortKind is what sort of interface a port is.
type PortKind string

// The kinds a source reports.
const (
	PortWired   PortKind = "wired"
	PortWiFi    PortKind = "wifi"
	PortVirtual PortKind = "virtual"
)

// Port is one of a source's interfaces and the VLANs it carries.
type Port struct {
	Name     string
	Kind     PortKind
	PVID     int
	Tagged   []int
	Untagged []int
}

// Sighting is one hardware address a source learned on one of its ports.
type Sighting struct {
	Port string
	MAC  string

	// VLAN is zero when the source did not say.
	VLAN int

	WiFi bool
	SSID string
	Band string

	// LastSeen is the last read that listed the address. A sighting from the
	// source's latest read is current; an older one is where the address was
	// last seen.
	LastSeen time.Time
}

// Neighbour is a device that announced itself on one of a source's ports.
type Neighbour struct {
	Port     string
	MAC      string
	Identity string
	Board    string
}

// Device is an inventory device the tree can place by its hardware address.
type Device struct {
	ID     int64
	MAC    string
	Name   string
	Class  string
	Online bool

	// VLAN is the VLAN of the network the device holds an address on, zero
	// when unknown. It stands in when no source said which VLAN it saw the
	// device in.
	VLAN int
}

// NodeKind is how the tree knows a switching device is there.
type NodeKind string

// A node Jocasta reads, one that only announced itself to a device Jocasta
// reads, one inferred from several devices sharing one port, and a
// hypervisor on a port it shares with its virtual machines.
const (
	NodeRead    NodeKind = "read"
	NodeSeen    NodeKind = "seen"
	NodeUnnamed NodeKind = "unnamed"
	NodeHost    NodeKind = "host"
)

// Tree is the whole network, from the router down.
type Tree struct {
	// Root is the router, or nil when no source has been read.
	Root *Node

	// Unplaced is the online devices no source has seen on any port, in name
	// order.
	Unplaced []*Leaf

	// VLANs is every VLAN a placed device is in, in order.
	VLANs []int

	nodeByDevice map[int64]*Node
	leafByDevice map[int64]*Leaf
}

// Node is a router, switch, access point or hypervisor.
type Node struct {
	Kind NodeKind

	// Key identifies the node across reads, for a page that keeps its
	// selection through a refresh.
	Key string

	// Name is the device's identity, or its inventory name, and empty for an
	// unnamed switch.
	Name  string
	Board string

	// DeviceID is the inventory device this node is, zero when there is none.
	DeviceID int64
	Online   bool

	// WiFi marks an unnamed node whose devices are all on Wi-Fi: an access
	// point nothing announced.
	WiFi bool

	// VMs marks an unnamed node whose devices are mostly virtual machines: a
	// hypervisor the inventory does not know as one.
	VMs bool

	// Uplink is how the node hangs from Parent, nil on the root.
	Uplink *Link
	Parent *Node

	// Children are the switches and access points below this node, in port
	// order.
	Children []*Node

	// Groups are the devices on this node, wired first, each in VLAN order,
	// then one group per Wi-Fi network.
	Groups []*Group
}

// Link is how a node hangs from its parent.
type Link struct {
	// ParentPort is the parent's port the node is on, and Port the node's own
	// port facing the parent. Either is empty when unknown.
	ParentPort string
	Port       string

	// VLANs is what the parent's port carries. Trunk marks a port carrying
	// tagged VLANs.
	VLANs []int
	Trunk bool
}

// Group is the devices on a node in one VLAN: the wired ones, or the ones on
// one Wi-Fi network. Each device says which port it is on.
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

	// VLANs is every VLAN the device was seen in, VLAN the one it is grouped
	// under.
	VLANs []int
	VLAN  int

	WiFi bool
	SSID string
	Band string

	// Port is the port of Owner the device is on, empty when unknown.
	Port  string
	Owner *Node

	// Current reports whether the latest read of Owner's source saw the
	// device. A device placed from an older read is where it was last seen.
	Current bool
}

// Node returns the node that is device id, and false when the device is not a
// node in the tree.
func (t *Tree) Node(id int64) (*Node, bool) {
	n, ok := t.nodeByDevice[id]

	return n, ok
}

// Leaf returns where device id is placed, and false when it is not.
func (t *Tree) Leaf(id int64) (*Leaf, bool) {
	l, ok := t.leafByDevice[id]

	return l, ok
}

// Path returns the nodes from the root down to n, inclusive.
func Path(n *Node) []*Node {
	var out []*Node

	for ; n != nil; n = n.Parent {
		out = append(out, n)
	}

	slices.Reverse(out)

	return out
}
