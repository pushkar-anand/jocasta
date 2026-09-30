// Package topomap lays out the topology page: where each router, switch,
// access point and group of devices goes on the canvas.
//
// The tree reads top down, like a drawing of a network on a whiteboard: the
// internet at the top, the router below it, then each switch and access point
// on the port it hangs from. Below each of those, its devices stand in a
// column per port or Wi-Fi network, beside the switches and access points on
// its other ports. A branch is as wide as everything below it, so nothing
// overlaps.
//
// The layout is a pure function of the tree, and the tree is ordered by port,
// so a device keeps its place from one refresh to the next.
package topomap

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/topology"
	"github.com/pushkar-anand/jocasta/internal/web/netmap"
)

const (
	nodeW = 190.0
	nodeH = 48.0

	chipW   = 190.0
	chipH   = 22.0
	chipGap = 4.0
	headH   = 26.0

	// slotGap is the room between two columns under one node, and levelGap
	// the drop from a node to what hangs below it, which holds the port
	// labels.
	slotGap  = 28.0
	levelGap = 64.0

	margin    = 40.0
	internetR = 20.0

	maxLabel = 26
)

// Point is a position on the canvas.
type Point struct{ X, Y float64 }

// Box is a router, switch or access point.
type Box struct {
	*topology.Node

	X, Y, W, H float64

	// Key is what selecting it is keyed on: its device's key when it is one,
	// and the node's own key otherwise.
	Key   string
	Label string

	// On is every key below it and its own, space-separated: selecting any
	// of them lights this box as part of the path.
	On string
}

// Group is a column of devices under one port or Wi-Fi network.
type Group struct {
	*topology.Group

	X, Y, W, H float64
	Title      string

	// Tone is the colour index of the group's VLAN, -1 when it has none.
	Tone  int
	Chips []Chip
	On    string
}

// Chip is one device in a group.
type Chip struct {
	*topology.Leaf

	X, Y  float64
	Key   string
	Label string

	// Tone is the colour index of the device's VLAN, -1 when it has none.
	Tone int
}

// Edge is a line from a node to what hangs below it.
type Edge struct {
	Path string

	// Kind is "trunk", "vlan" or "wifi", for how it is drawn.
	Kind string

	// Label names the parent's port, drawn at LabelAt.
	Label   string
	LabelAt Point
	Title   string
	On      string
}

// Layout is everything the template draws.
type Layout struct {
	Width, Height float64
	Internet      Point

	Boxes  []*Box
	Groups []*Group
	Edges  []*Edge
}

// Place lays out tree. A tree with nothing read has no layout.
func Place(tree *topology.Tree) *Layout {
	if tree == nil || tree.Root == nil {
		return nil
	}

	p := &placer{
		tones: map[int]int{},
		width: map[*topology.Node]float64{},
		out:   &Layout{},
	}

	for i, v := range tree.VLANs {
		p.tones[v] = i
	}

	w := p.measure(tree.Root)

	p.out.Internet = Point{margin + w/2, margin + internetR}
	top := margin + 2*internetR + levelGap

	root, _ := p.position(tree.Root, margin, top)

	p.out.Edges = append(p.out.Edges, &Edge{
		Path: fmt.Sprintf("M %s %s V %s", num(p.out.Internet.X), num(p.out.Internet.Y+internetR), num(top)),
		Kind: "trunk",
		On:   root.On,
	})

	p.out.Width = w + 2*margin
	p.out.Height = p.bottom + margin

	return p.out
}

type placer struct {
	tones  map[int]int
	width  map[*topology.Node]float64
	bottom float64
	out    *Layout
}

// tone returns the colour index of VLAN v, or -1 for a device in no known
// VLAN.
func (p *placer) tone(v int) int {
	if t, ok := p.tones[v]; ok {
		return t
	}

	return -1
}

// slot is one column under a node: a group of devices or a node's subtree.
type slot struct {
	port  string
	wifi  bool
	group *topology.Group
	node  *topology.Node
}

// slots returns what hangs below n in port order, wired before Wi-Fi.
func slots(n *topology.Node) []slot {
	out := make([]slot, 0, len(n.Groups)+len(n.Children))

	for _, g := range n.Groups {
		out = append(out, slot{port: g.Port, wifi: g.WiFi, group: g})
	}

	for _, c := range n.Children {
		out = append(out, slot{port: c.Uplink.ParentPort, node: c})
	}

	slices.SortStableFunc(out, func(a, b slot) int {
		switch {
		case a.wifi != b.wifi && !a.wifi:
			return -1
		case a.wifi != b.wifi:
			return 1
		}

		return topology.ComparePorts(a.port, b.port)
	})

	return out
}

// measure returns how wide n's subtree is.
func (p *placer) measure(n *topology.Node) float64 {
	ss := slots(n)

	for _, s := range ss {
		if s.node != nil {
			p.measure(s.node)
		}
	}

	w := max(nodeW, p.span(ss))
	p.width[n] = w

	return w
}

// span returns the width of slots ss side by side, from the widths measure
// recorded.
func (p *placer) span(ss []slot) float64 {
	total := 0.0

	for i, s := range ss {
		if i > 0 {
			total += slotGap
		}

		if s.node != nil {
			total += p.width[s.node]
		} else {
			total += chipW
		}
	}

	return total
}

// position places n's subtree with its left edge at left and n's box at top,
// returning n's box and the keys below it.
func (p *placer) position(n *topology.Node, left, top float64) (*Box, []string) {
	w := p.width[n]

	box := &Box{
		Node: n,
		X:    left + (w-nodeW)/2,
		Y:    top,
		W:    nodeW,
		H:    nodeH,
		Key:  nodeKey(n),
	}
	box.Label = shorten(nodeName(n))

	p.out.Boxes = append(p.out.Boxes, box)
	p.bottom = max(p.bottom, top+nodeH)

	ss := slots(n)
	x := left + (w-p.span(ss))/2
	below := top + nodeH + levelGap
	from := Point{box.X + nodeW/2, top + nodeH}

	keys := []string{box.Key}

	for _, s := range ss {
		if s.node != nil {
			child, under := p.position(s.node, x, below)
			keys = append(keys, under...)

			p.out.Edges = append(p.out.Edges, p.nodeEdge(from, child, s.node.Uplink))

			x += p.width[s.node] + slotGap

			continue
		}

		g, under := p.group(s.group, x, below)
		keys = append(keys, under...)

		p.out.Edges = append(p.out.Edges, &Edge{
			Path:  elbow(from, Point{g.X + g.W/2, g.Y}),
			Kind:  groupKind(s.group),
			Title: g.Title,
			On:    g.On,
		})

		x += chipW + slotGap
	}

	box.On = strings.Join(keys, " ")

	return box, keys
}

// nodeEdge draws the line from a parent at from down to child, labelled with
// the parent's port.
func (p *placer) nodeEdge(from Point, child *Box, link *topology.Link) *Edge {
	to := Point{child.X + child.W/2, child.Y}

	e := &Edge{
		Path:    elbow(from, to),
		Kind:    "vlan",
		On:      child.On,
		LabelAt: Point{to.X + 6, to.Y - 8},
	}

	if link == nil {
		return e
	}

	vlans := vlanList(link.VLANs)

	switch {
	case link.Trunk:
		e.Kind = "trunk"
		e.Label = join(link.ParentPort, "trunk")
		e.Title = join(link.ParentPort, "VLANs "+vlans)
	case vlans != "":
		e.Label = join(link.ParentPort, "VLAN "+vlans)
		e.Title = e.Label
	default:
		e.Label = link.ParentPort
		e.Title = e.Label
	}

	if child.WiFi && child.Kind == topology.NodeUnnamed {
		e.Kind = "wifi"
	}

	return e
}

// group places one column of devices with its top left at (left, top).
func (p *placer) group(g *topology.Group, left, top float64) (*Group, []string) {
	out := &Group{
		Group: g,
		X:     left,
		Y:     top,
		W:     chipW,
		H:     headH + float64(len(g.Devices))*(chipH+chipGap),
		Title: groupTitle(g),
		Tone:  p.tone(g.VLAN),
	}

	keys := make([]string, 0, len(g.Devices))

	for i, l := range g.Devices {
		c := Chip{
			Leaf:  l,
			X:     left,
			Y:     top + headH + float64(i)*(chipH+chipGap),
			Key:   netmap.DeviceKey(l.DeviceID),
			Label: shorten(l.Name),
			Tone:  p.tone(l.VLAN),
		}

		out.Chips = append(out.Chips, c)
		keys = append(keys, c.Key)
	}

	out.On = strings.Join(keys, " ")

	p.out.Groups = append(p.out.Groups, out)
	p.bottom = max(p.bottom, top+out.H)

	return out, keys
}

func groupKind(g *topology.Group) string {
	if g.WiFi {
		return "wifi"
	}

	return "vlan"
}

// groupTitle names a group by its port or Wi-Fi network and its VLAN.
func groupTitle(g *topology.Group) string {
	where := g.Port
	if g.WiFi {
		where = g.SSID
	}

	vlan := ""
	if g.VLAN > 0 {
		vlan = "VLAN " + strconv.Itoa(g.VLAN)
	}

	return join(where, vlan)
}

// nodeKey is the key a node is selected by.
func nodeKey(n *topology.Node) string {
	if n.DeviceID != 0 {
		return netmap.DeviceKey(n.DeviceID)
	}

	return n.Key
}

// nodeName is what a node is called on the page.
func nodeName(n *topology.Node) string {
	switch {
	case n.Name != "":
		return n.Name
	case n.WiFi:
		return "Access point"
	case n.VMs:
		return "Hypervisor"
	default:
		return "Switch"
	}
}

// elbow is a line down from a, across, and down into b, so branches read as a
// tree and never cross a box.
func elbow(a, b Point) string {
	mid := a.Y + levelGap/2

	if a.X == b.X {
		return fmt.Sprintf("M %s %s V %s", num(a.X), num(a.Y), num(b.Y))
	}

	return fmt.Sprintf("M %s %s V %s H %s V %s", num(a.X), num(a.Y), num(mid), num(b.X), num(b.Y))
}

func join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}

	return a + " · " + b
}

func vlanList(vlans []int) string {
	parts := make([]string, len(vlans))
	for i, v := range vlans {
		parts[i] = strconv.Itoa(v)
	}

	return strings.Join(parts, ", ")
}

// shorten cuts a label that would run into its neighbour's column.
func shorten(s string) string {
	r := []rune(s)
	if len(r) <= maxLabel {
		return s
	}

	return string(r[:maxLabel-1]) + "…"
}

// num renders a coordinate to one decimal place, which keeps the SVG short.
func num(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }
