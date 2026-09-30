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
	"cmp"
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

	// legendRoom is kept clear on the right, where the page floats its
	// legend over the canvas.
	legendRoom = 260.0

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

	// Title is the device's full name and where it is, for its tooltip.
	Title string

	// Aside is the port a wired device is on, or the band of a Wi-Fi one,
	// drawn at AsideAt on the chip's right.
	Aside   string
	AsideAt Point

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

	p.out.Width = w + 2*margin + legendRoom
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

// slots returns what hangs below n: its wired devices, the nodes on its ports
// in port order, then its Wi-Fi networks.
func slots(n *topology.Node) []slot {
	out := make([]slot, 0, len(n.Groups)+len(n.Children))

	for _, g := range n.Groups {
		out = append(out, slot{wifi: g.WiFi, group: g})
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
	box.Label = shorten(NodeName(n), maxLabel)

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

	e.Label = LinkLabel(link)
	e.Title = e.Label

	switch {
	case link.Trunk:
		e.Kind = "trunk"
		e.Title = join(link.ParentPort, "VLANs\u00a0"+vlanList(link.VLANs))
	case child.WiFi && child.Kind == topology.NodeUnnamed:
		e.Kind = "wifi"
	}

	return e
}

// LinkLabel names the parent's port a node hangs from and what it carries:
// "sfp1 · trunk", or "ether4 · VLAN 10" for a port carrying one VLAN.
func LinkLabel(link *topology.Link) string {
	switch {
	case link.Trunk:
		return join(link.ParentPort, "trunk")
	case len(link.VLANs) > 0:
		return join(link.ParentPort, "VLAN\u00a0"+vlanList(link.VLANs))
	default:
		return link.ParentPort
	}
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
			Label: shorten(l.Name, maxLabel),
			Title: chipTitle(l),
			Tone:  p.tone(l.VLAN),
		}

		switch {
		case l.WiFi:
			c.Aside = Band(l.Band)
		default:
			c.Aside = l.Port
		}

		if c.Aside != "" {
			c.AsideAt = Point{c.X + chipW - 8, c.Y}
			c.Label = shorten(l.Name, maxLabel-len([]rune(c.Aside))-2)
		}

		out.Chips = append(out.Chips, c)
		keys = append(keys, c.Key)
	}

	out.On = strings.Join(keys, " ")

	p.out.Groups = append(p.out.Groups, out)
	p.bottom = max(p.bottom, top+out.H)

	return out, keys
}

// chipTitle says what a device is and where, and whether that is only where it
// was last seen.
func chipTitle(l *topology.Leaf) string {
	parts := []string{l.Name}

	if where := Where(l); where != "" {
		parts = append(parts, where)
	}

	if !l.Current {
		parts = append(parts, "last seen here")
	}

	return strings.Join(parts, " · ")
}

// Where says where a device is on the node it hangs from: its port, or its
// Wi-Fi network and band, then its VLANs.
func Where(l *topology.Leaf) string {
	var parts []string

	switch {
	case l.WiFi:
		parts = append(parts, join(cmp.Or(l.SSID, "Wi-Fi"), Band(l.Band)))
	case l.Port != "":
		parts = append(parts, l.Port)
	}

	if len(l.VLANs) > 1 {
		parts = append(parts, "VLANs\u00a0"+vlanList(l.VLANs))
	} else if l.VLAN > 0 {
		parts = append(parts, "VLAN\u00a0"+strconv.Itoa(l.VLAN))
	}

	return strings.Join(parts, " · ")
}

// Band says a Wi-Fi band the way people do: RouterOS's "5ghz-ax" is "5 GHz",
// and "2ghz-n" is "2.4 GHz". A band it does not recognise comes back as it is.
func Band(band string) string {
	ghz, _, ok := strings.Cut(strings.ToLower(band), "ghz")
	if !ok {
		return band
	}

	if ghz == "2" {
		ghz = "2.4"
	}

	return ghz + " GHz"
}

func groupKind(g *topology.Group) string {
	if g.WiFi {
		return "wifi"
	}

	return "vlan"
}

// groupTitle names a group by its Wi-Fi network, or as wired, and its VLAN.
func groupTitle(g *topology.Group) string {
	where := "Wired"
	if g.WiFi {
		where = cmp.Or(g.SSID, "Wi-Fi")
	}

	if g.VLAN == 0 {
		return where
	}

	return join(where, "VLAN\u00a0"+strconv.Itoa(g.VLAN))
}

// nodeKey is the key a node is selected by.
func nodeKey(n *topology.Node) string {
	if n.DeviceID != 0 {
		return netmap.DeviceKey(n.DeviceID)
	}

	return n.Key
}

// NodeName is what a node is called on the page: its name, or what it is when
// it has none.
func NodeName(n *topology.Node) string {
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

// shorten cuts a label longer than n runes, so it stays inside its box.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}

// num renders a coordinate to one decimal place, which keeps the SVG short.
func num(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }
