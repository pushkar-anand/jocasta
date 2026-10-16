// Package topomap lays out the topology page: where each router, switch, access
// point and group of devices goes on the canvas.
//
// The tree reads from the top down, as a drawing of a network on a whiteboard.
// The internet is at the top, and the router below it. Then each switch and
// access point is on the port from which it hangs. Below each, its devices are
// in a column for each port or Wi-Fi network. They are next to the switches
// and access points on its other ports. A branch is as wide as everything
// below it. So nothing overlaps.
//
// The layout is a pure function of the tree, and the tree is ordered by port.
// So a device keeps its place from one refresh to the next.
package topomap

import (
	"cmp"
	"fmt"
	"math"
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

	// slotGap is the space between two columns under one node. levelGap is
	// the drop from a node to what hangs below it. The port labels are in it.
	slotGap  = 28.0
	levelGap = 64.0

	// groupColumn is the most devices that a group lists in one column. After
	// it, the chips wrap into columns colGap apart. So a long group stays
	// about as wide as it is tall, and does not draw as a thin strip.
	groupColumn = 24
	colGap      = 8.0

	margin    = 40.0
	internetR = 20.0

	// legendRoom stays clear on the right, where the page puts its legend
	// over the canvas.
	legendRoom = 260.0

	maxLabel = 26
)

// Point is a position on the canvas.
type Point struct{ X, Y float64 }

// Box is a router, switch or access point.
type Box struct {
	*topology.Node

	X, Y, W, H float64

	// Key is the key for its selection. It is the key of its device, if it is
	// one. Else, it is the own key of the node.
	Key   string
	Label string

	// On is every key below it, and its own, space-separated. A selection of
	// any of them highlights this box as part of the path.
	On string
}

// Group is a column of devices under one port or Wi-Fi network.
type Group struct {
	*topology.Group

	X, Y, W, H float64
	Title      string

	// Tone is the colour index of the VLAN of the group. It is -1 when the
	// group has none.
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

	// Title is the full name of the device and where it is, for its
	// tooltip.
	Title string

	// Aside is the port of a wired device, or the band of a Wi-Fi device. It
	// is drawn at AsideAt, on the right of the chip.
	Aside   string
	AsideAt Point

	// Tone is the colour index of the VLAN of the device. It is -1 when the
	// device has none.
	Tone int
}

// Edge is a line from a node to what hangs below it.
type Edge struct {
	Path string

	// Kind is "trunk", "vlan" or "wifi". It sets how the edge is drawn.
	Kind string

	// Label names the port of the parent, drawn at LabelAt.
	Label   string
	LabelAt Point
	Title   string
	On      string
}

// Layout is everything that the template draws.
type Layout struct {
	Width, Height float64
	Internet      Point

	Boxes  []*Box
	Groups []*Group
	Edges  []*Edge
}

// Place lays out tree. A tree with no reads has no layout.
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

// tone returns the colour index of VLAN v. It returns -1 for a device in no
// known VLAN.
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
// in port order, and then its Wi-Fi networks.
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

// measure returns the width of the subtree of n.
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

// span returns the width of slots ss next to each other, from the widths that
// measure recorded.
func (p *placer) span(ss []slot) float64 {
	total := 0.0

	for i, s := range ss {
		if i > 0 {
			total += slotGap
		}

		if s.node != nil {
			total += p.width[s.node]
		} else {
			total += groupWidth(s.group)
		}
	}

	return total
}

// position places the subtree of n, with its left edge at left, and the box of
// n at top. It returns the box of n and the keys below it.
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

		x += g.W + slotGap
	}

	box.On = strings.Join(keys, " ")

	return box, keys
}

// nodeEdge draws the line from a parent at from down to child. The label is the
// port of the parent.
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

// LinkLabel names the port of the parent from which a node hangs, and what it
// carries: "sfp1 · trunk", or "ether4 · VLAN 10" for a port with one VLAN.
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

// groupColumns is how many columns the chips of g take. It is one up to
// groupColumn devices. Then it is as many as keep the group about as wide as
// it is tall.
func groupColumns(g *topology.Group) int {
	n := len(g.Devices)
	if n <= groupColumn {
		return 1
	}

	return max(2, int(math.Round(math.Sqrt(float64(n)*(chipH+chipGap)/(chipW+colGap)))))
}

// groupWidth is the drawn width of g.
func groupWidth(g *topology.Group) float64 {
	cols := float64(groupColumns(g))

	return cols*chipW + (cols-1)*colGap
}

// group places the devices of g in columns, with its top left at (left,
// top).
func (p *placer) group(g *topology.Group, left, top float64) (*Group, []string) {
	cols := groupColumns(g)
	rows := (len(g.Devices) + cols - 1) / cols

	out := &Group{
		Group: g,
		X:     left,
		Y:     top,
		W:     groupWidth(g),
		H:     headH + float64(rows)*(chipH+chipGap),
		Title: groupTitle(g),
		Tone:  p.tone(g.VLAN),
	}

	keys := make([]string, 0, len(g.Devices))

	for i, l := range g.Devices {
		c := Chip{
			Leaf:  l,
			X:     left + float64(i/rows)*(chipW+colGap),
			Y:     top + headH + float64(i%rows)*(chipH+chipGap),
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

// chipTitle tells what a device is and where it is. It also tells whether that
// is only where a source last saw it.
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

// Where tells where a device is on the node from which it hangs: its port, or
// its Wi-Fi network and band, then its VLANs.
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

// Band writes a Wi-Fi band as people say it. "5ghz-ax" of RouterOS is "5 GHz",
// and "2ghz-n" is "2.4 GHz". A band that it does not know comes back as it
// is.
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

// groupTitle names a group by its Wi-Fi network, or as wired, with its VLAN.
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

// nodeKey is the key for the selection of a node.
func nodeKey(n *topology.Node) string {
	if n.DeviceID != 0 {
		return netmap.DeviceKey(n.DeviceID)
	}

	return n.Key
}

// NodeName is the name of a node on the page. It is its name, or what it is
// when it has no name.
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

// elbow is a line down from a, across, and down into b. So branches look like a
// tree, and never cross a box.
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

// shorten cuts a label longer than n runes. So it stays inside its box.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}

// num renders a coordinate to one decimal place. This keeps the SVG short.
func num(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }
