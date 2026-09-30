package mcp

import (
	"context"
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/topology"
)

// link is how fast a wired link runs, as the latest read of the port found it.
type link struct {
	RateBps    int64 `json:"rate_bps" jsonschema:"The rate the link came up at, in bits per second."`
	CapableBps int64 `json:"capable_bps,omitempty" jsonschema:"The fastest rate both ends offer, in bits per second, where the port reported it."`
	FullDuplex bool  `json:"full_duplex"`
	Slow       bool  `json:"slow" jsonschema:"The link came up below the rate both ends can run at. A damaged or poor cable is the usual cause."`
}

// linkOf is s as a tool reports it, nil when its rate is unknown.
func linkOf(s topology.Speed) *link {
	if s.Rate == 0 {
		return nil
	}

	return &link{RateBps: s.Rate, CapableBps: s.Capable, FullDuplex: s.FullDuplex, Slow: s.Slow()}
}

// radio is how a Wi-Fi client's connection runs, as the latest read of the
// access point found it.
type radio struct {
	DownBps   int64 `json:"down_bps,omitempty" jsonschema:"The rate the access point sends to the client at, in bits per second."`
	UpBps     int64 `json:"up_bps,omitempty" jsonschema:"The rate the access point receives from the client at, in bits per second."`
	SignalDBm int   `json:"signal_dbm,omitempty" jsonschema:"The client's signal at the access point, in dBm. Closer to zero is stronger; below -75 is weak."`
}

// radioOf is r as a tool reports it, nil when nothing about it is known.
func radioOf(r topology.Radio) *radio {
	if r == (topology.Radio{}) {
		return nil
	}

	return &radio{DownBps: r.Down, UpBps: r.Up, SignalDBm: r.Signal}
}

// hop is one router, switch, access point or hypervisor on the path down to a
// device.
type hop struct {
	Name       string `json:"name,omitempty" jsonschema:"The node's own name for itself, or its inventory name. Empty for a node nothing named."`
	Kind       string `json:"kind" jsonschema:"How Jocasta knows the node: read (Jocasta reads its tables), seen (it announced itself to one Jocasta reads), unnamed (several devices share one port of a read node, so something sits there) or host (a hypervisor sharing a port with its virtual machines)."`
	Likely     string `json:"likely,omitempty" jsonschema:"For an unnamed node, what it most likely is: access_point when every device on it is on Wi-Fi, hypervisor when most are virtual machines, switch otherwise."`
	DeviceID   int64  `json:"device_id,omitempty" jsonschema:"The inventory device the node is, for get_device. Absent when the inventory has none."`
	ParentPort string `json:"parent_port,omitempty" jsonschema:"The port of the hop before this one that this node hangs from. Absent on the router and when unknown."`
}

// hopOf is n as a tool reports it on a path.
func hopOf(n *topology.Node) hop {
	h := hop{Name: n.Name, Kind: string(n.Kind), Likely: likely(n), DeviceID: n.DeviceID}

	if n.Uplink != nil {
		h.ParentPort = n.Uplink.ParentPort
	}

	return h
}

// likely is what an unnamed node most likely is, empty for any other node.
func likely(n *topology.Node) string {
	switch {
	case n.Kind != topology.NodeUnnamed:
		return ""
	case n.WiFi:
		return "access_point"
	case n.VMs:
		return "hypervisor"
	default:
		return "switch"
	}
}

// connection is where a device is plugged in: the path down from the router,
// and how the device hangs from the end of it.
type connection struct {
	Placed  bool  `json:"placed" jsonschema:"Whether any router, switch or access point Jocasta reads has seen the device on a port. Every other field is absent when not."`
	Current bool  `json:"current,omitempty" jsonschema:"Whether the latest reads saw the device there. False means this is where it was last seen."`
	Path    []hop `json:"path,omitempty" jsonschema:"The nodes from the router down to the one the device hangs from, in that order."`

	Port  string `json:"port,omitempty" jsonschema:"The last hop's port the device is on. Absent when unknown."`
	VLANs []int  `json:"vlans,omitempty" jsonschema:"The VLANs the device was seen in, or that its port carries for a switch or access point."`
	Trunk bool   `json:"trunk,omitempty" jsonschema:"For a switch or access point: the port it hangs from carries tagged VLANs."`

	WiFi bool   `json:"wifi,omitempty"`
	SSID string `json:"ssid,omitempty"`
	Band string `json:"band,omitempty" jsonschema:"The Wi-Fi band as the access point names it, such as 5ghz-ax."`

	Link  *link  `json:"link,omitempty" jsonschema:"How fast the device's own wired link runs. Absent when unknown, and for a device behind a switch or hypervisor Jocasta does not read, whose port speed belongs to that node's link."`
	Radio *radio `json:"radio,omitempty" jsonschema:"How the device's Wi-Fi connection runs."`
}

// connectionOf returns where device id sits in tree.
func connectionOf(tree *topology.Tree, id int64) *connection {
	if node, ok := tree.Node(id); ok {
		path := topology.Path(node)
		c := &connection{Placed: true, Current: true, Path: hops(path[:len(path)-1])}

		if up := node.Uplink; up != nil {
			c.Port, c.VLANs, c.Trunk, c.Link = up.ParentPort, up.VLANs, up.Trunk, linkOf(up.Speed)
		}

		return c
	}

	leaf, ok := tree.Leaf(id)
	if !ok {
		return &connection{}
	}

	return &connection{
		Placed:  true,
		Current: leaf.Current,
		Path:    hops(topology.Path(leaf.Owner)),
		Port:    leaf.Port,
		VLANs:   vlansOf(leaf),
		WiFi:    leaf.WiFi,
		SSID:    leaf.SSID,
		Band:    leaf.Band,
		Link:    linkOf(leaf.Speed),
		Radio:   radioOf(leaf.Radio),
	}
}

// hops is each node on a path as a tool reports it.
func hops(path []*topology.Node) []hop {
	out := make([]hop, len(path))
	for i, n := range path {
		out[i] = hopOf(n)
	}

	return out
}

// vlansOf is every VLAN l was seen in, or the VLAN of its network when no
// source said.
func vlansOf(l *topology.Leaf) []int {
	if len(l.VLANs) == 0 && l.VLAN > 0 {
		return []int{l.VLAN}
	}

	return l.VLANs
}

// topologyNode is one router, switch, access point or hypervisor in the tree.
type topologyNode struct {
	Key      string `json:"key" jsonschema:"Identifies the node within this result. Devices and child nodes name their node by it."`
	Name     string `json:"name,omitempty" jsonschema:"The node's own name for itself, or its inventory name. Empty for a node nothing named."`
	Kind     string `json:"kind" jsonschema:"How Jocasta knows the node: read (Jocasta reads its tables), seen (it announced itself to one Jocasta reads), unnamed (several devices share one port of a read node, so something sits there) or host (a hypervisor sharing a port with its virtual machines)."`
	Likely   string `json:"likely,omitempty" jsonschema:"For an unnamed node, what it most likely is: access_point when every device on it is on Wi-Fi, hypervisor when most are virtual machines, switch otherwise."`
	Board    string `json:"board,omitempty" jsonschema:"The hardware model the node announced."`
	DeviceID int64  `json:"device_id,omitempty" jsonschema:"The inventory device the node is, for get_device. Absent when the inventory has none."`
	Online   bool   `json:"online"`

	Parent     string `json:"parent,omitempty" jsonschema:"The key of the node this one hangs from. Absent on the router."`
	ParentPort string `json:"parent_port,omitempty" jsonschema:"The parent's port this node hangs from. Absent when unknown."`
	Port       string `json:"port,omitempty" jsonschema:"This node's own port facing its parent. Absent when unknown."`
	VLANs      []int  `json:"vlans,omitempty" jsonschema:"The VLANs the parent's port carries."`
	Trunk      bool   `json:"trunk,omitempty" jsonschema:"The parent's port carries tagged VLANs."`
	Link       *link  `json:"link,omitempty" jsonschema:"How fast the link to the parent runs. Absent when unknown."`
}

// placedDevice is one device and where it sits in the tree.
type placedDevice struct {
	DeviceID int64  `json:"device_id"`
	Name     string `json:"name"`
	Class    string `json:"class,omitempty"`
	Online   bool   `json:"online"`

	Node    string `json:"node" jsonschema:"The key of the node the device hangs from."`
	Current bool   `json:"current" jsonschema:"Whether the latest read of that node saw the device there. False means this is where it was last seen."`
	Port    string `json:"port,omitempty" jsonschema:"The node's port the device is on. Absent when unknown."`
	VLANs   []int  `json:"vlans,omitempty"`

	WiFi bool   `json:"wifi,omitempty"`
	SSID string `json:"ssid,omitempty"`
	Band string `json:"band,omitempty" jsonschema:"The Wi-Fi band as the access point names it, such as 5ghz-ax."`

	Link  *link  `json:"link,omitempty" jsonschema:"How fast the device's own wired link runs. Absent when unknown, and for a device behind a node Jocasta does not read, whose port speed belongs to that node's link."`
	Radio *radio `json:"radio,omitempty" jsonschema:"How the device's Wi-Fi connection runs."`
}

// unplacedDevice is an online device no source has seen on any port.
type unplacedDevice struct {
	DeviceID int64  `json:"device_id"`
	Name     string `json:"name"`
	Class    string `json:"class,omitempty"`
}

// getTopologyOutput is the whole tree, flattened: the nodes from the router
// down, each after the node it hangs from, and the devices on each.
type getTopologyOutput struct {
	// Recorded is false when no router, switch or access point has been read,
	// and the lists are empty.
	Recorded bool             `json:"recorded"`
	Nodes    []topologyNode   `json:"nodes"`
	Devices  []placedDevice   `json:"devices"`
	Unplaced []unplacedDevice `json:"unplaced"`
}

// getTopology is inventory.Store.Topology, offered as a tool.
func getTopology(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "get_topology",
		Title: "Get the network's topology",
		Description: "Get what is plugged in where across the whole network, from the tables of the routers, switches and " +
			"access points Jocasta reads. Returns the nodes (router, switches, access points, hypervisors) from the router " +
			"down, each with the node and port it hangs from, the VLANs that port carries and how fast its link runs; " +
			"every device placed on a node, with its port or Wi-Fi network, VLAN, and link speed or Wi-Fi rates and " +
			"signal; and the online devices no port has seen. Ignored devices are left out. " +
			"recorded is false when nothing has been read yet. For one device, get_device says the same about it.",
		InputSchema:  schemaFor[struct{}](),
		OutputSchema: schemaFor[getTopologyOutput](),
		Annotations:  readOnly(),
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		_ struct{},
	) (*mcpsdk.CallToolResult, getTopologyOutput, error) {
		tree, err := store.Topology(ctx)
		if err != nil {
			return nil, getTopologyOutput{}, err
		}

		return nil, topologyOf(tree), nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// topologyOf flattens tree into what get_topology answers with.
func topologyOf(tree *topology.Tree) getTopologyOutput {
	out := getTopologyOutput{
		Recorded: tree.Root != nil,
		Nodes:    []topologyNode{},
		Devices:  []placedDevice{},
		Unplaced: make([]unplacedDevice, 0, len(tree.Unplaced)),
	}

	if tree.Root != nil {
		out.walk(tree.Root)
	}

	for _, l := range tree.Unplaced {
		out.Unplaced = append(out.Unplaced, unplacedDevice{DeviceID: l.DeviceID, Name: l.Name, Class: l.Class})
	}

	return out
}

// walk adds n, the devices on it, and every node below it, in that order.
func (o *getTopologyOutput) walk(n *topology.Node) {
	node := topologyNode{
		Key:      n.Key,
		Name:     n.Name,
		Kind:     string(n.Kind),
		Likely:   likely(n),
		Board:    n.Board,
		DeviceID: n.DeviceID,
		Online:   n.Online,
	}

	if n.Parent != nil {
		node.Parent = n.Parent.Key
	}

	if up := n.Uplink; up != nil {
		node.ParentPort, node.Port, node.VLANs, node.Trunk, node.Link = up.ParentPort, up.Port, up.VLANs, up.Trunk, linkOf(up.Speed)
	}

	o.Nodes = append(o.Nodes, node)

	for _, g := range n.Groups {
		for _, l := range g.Devices {
			o.Devices = append(o.Devices, placedDevice{
				DeviceID: l.DeviceID,
				Name:     l.Name,
				Class:    l.Class,
				Online:   l.Online,
				Node:     n.Key,
				Current:  l.Current,
				Port:     l.Port,
				VLANs:    vlansOf(l),
				WiFi:     l.WiFi,
				SSID:     l.SSID,
				Band:     l.Band,
				Link:     linkOf(l.Speed),
				Radio:    radioOf(l.Radio),
			})
		}
	}

	for _, c := range n.Children {
		o.walk(c)
	}
}
