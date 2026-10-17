// Package netmap lays out the traffic map: where each device, organisation and
// line goes on the canvas.
//
// The map is a tree. The router is in the centre. Around it, there is a hub for
// each network, and one for the internet. Around each hub are its devices, or
// the organisations of the internet. The lines are the branches. A branch is
// thicker when more traffic went along it. Over the tree, a link joins each
// device to what it exchanged traffic with: an organisation, or a device on a
// different network. The link has its services.
//
// The layout is a pure function of its input, ordered by id. So a device keeps
// its place from one refresh to the next, for any traffic. The picture changes
// only where the network changed.
package netmap

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

const (
	// nodeSpacing is the distance between neighbours around a hub. It is
	// enough, so the labels that point away from it do not touch.
	nodeSpacing = 24.0

	// minCluster is the smallest radius of the nodes of a hub.
	minCluster = 70.0

	// labelGap is how far after its node a label starts. labelSpan is the
	// space that a label takes. Both keep clusters apart.
	labelGap  = 12.0
	labelSpan = 170.0

	// routerGap is the angle that a hub leaves empty on the side towards the
	// router, where its branch comes in.
	routerGap = 50 * math.Pi / 180

	// minHubRing is the smallest distance from the router to a hub. margin is
	// the empty edge around the whole map.
	minHubRing = 260.0
	margin     = 40.0

	// wide stretches the ring of hubs across, because the screen of the map is
	// wider than it is tall.
	wide = 1.4

	// hubLabelGap is the distance of the name of a hub from the hub, towards
	// the router. The name is in the gap that its devices leave, when it runs
	// along its branch.
	hubLabelGap = 34.0

	maxLabel = 24

	minStroke = 0.75
	maxStroke = 6.0

	// labelAlong is how far along a link the names of its services are. They
	// are towards the peer, away from the router that the link bends past.
	labelAlong = 0.7

	// linkScale makes the links thinner than the branches. So the tree is
	// still readable under them.
	linkScale = 0.6
)

// Point is a position on the canvas.
type Point struct{ X, Y float64 }

// Segment is one network that groups the devices, in the order of the map. ID
// zero holds the devices on no recorded network.
type Segment struct {
	ID    int64
	Label string
}

// Label is text next to a node, turned to point away from its hub.
type Label struct {
	At     Point
	Rotate float64
	Anchor string // "start" or "end"
	Text   string
}

// Hub is the node of a network, or of the internet, between the router and
// what hangs from it.
type Hub struct {
	Label    string
	At       Point
	LabelAt  Point
	Anchor   string // how its name is aligned on LabelAt
	Hangs    string // "down" or "up", the way a name across its branch grows from LabelAt
	Index    int    // which network, for its colour
	Internet bool
	Active   bool

	// Nodes counts the devices, or organisations, around it.
	Nodes int
}

// Device is a device around the hub of its network.
type Device struct {
	*inventory.MapDevice

	At     Point
	Label  Label
	Sector int
}

// Org is an organisation around the hub of the internet.
type Org struct {
	*inventory.MapOrg

	At    Point
	Label Label
}

// Line is one branch of the tree.
type Line struct {
	Path   string
	Width  float64
	Active bool
	Title  string
}

// Link is traffic between two nodes, drawn over the tree. A and B are the keys
// of its ends, from DeviceKey and OrgKey.
type Link struct {
	Path     string
	Width    float64
	Active   bool
	A, B     string
	Title    string
	LabelAt  Point
	Services []inventory.MapService
}

// Layout is everything that the template draws.
type Layout struct {
	Width, Height float64
	Router        Point

	Hubs    []Hub
	Devices []Device
	Orgs    []Org
	Lines   []Line
	Links   []Link
}

// DeviceKey names a device for the links that end on it.
func DeviceKey(id int64) string { return "d" + strconv.FormatInt(id, 10) }

// OrgKey names an organisation for the links that end on it.
func OrgKey(number uint32) string { return "o" + strconv.FormatUint(uint64(number), 10) }

// Key is the name of the device for its links.
func (d Device) Key() string { return DeviceKey(d.ID) }

// Key is the name of the organisation for its links.
func (o Org) Key() string { return OrgKey(o.ASN) }

// cluster is one hub with what hangs from it, before it is placed.
type cluster struct {
	hub     Hub
	devices []*inventory.MapDevice
	orgs    []*inventory.MapOrg
	bytes   int64

	// radius is the distance of its nodes. reach is how far from the hub its
	// labels end.
	radius, reach float64

	angle float64
}

func (c *cluster) size() int { return len(c.devices) + len(c.orgs) }

// Place lays out m, with its devices grouped into segments, in their order. A
// device whose network is not among them goes in segment zero.
func Place(m *inventory.TrafficMap, segments []Segment) *Layout {
	clusters := buildClusters(m, segments)
	if len(clusters) == 0 {
		return &Layout{Width: 2 * minHubRing, Height: 2 * minHubRing, Router: Point{minHubRing, minHubRing}}
	}

	// Each hub gets a part of the circle by the reach of its cluster. The ring
	// is as big as necessary, so neighbours do not meet. The internet comes
	// first, centred to the right.
	var need, widest float64

	for _, c := range clusters {
		c.radius = max(minCluster, float64(c.size())*nodeSpacing/(2*math.Pi-routerGap))
		c.reach = c.radius + labelGap + labelSpan
		need += 2 * c.reach
		widest = max(widest, c.reach)
	}

	ring := max(minHubRing, need*1.05/(2*math.Pi), widest+80)

	at := -clusters[0].reach / need * 2 * math.Pi
	for _, c := range clusters {
		share := 2 * c.reach / need * 2 * math.Pi
		c.angle = at + share/2
		at += share
	}

	across, down := ring*wide+widest+margin, ring+widest+margin
	router := Point{across, down}
	l := &Layout{Width: round(2 * across), Height: round(2 * down), Router: router}

	var hubTop, nodeTop int64 = 1, 1

	for _, c := range clusters {
		hubTop = max(hubTop, c.bytes)

		for _, d := range c.devices {
			nodeTop = max(nodeTop, d.Bytes)
		}

		for _, o := range c.orgs {
			nodeTop = max(nodeTop, o.Bytes)
		}
	}

	for _, c := range clusters {
		hub := Point{
			X: round(router.X + ring*wide*math.Cos(c.angle)),
			Y: round(router.Y + ring*math.Sin(c.angle)),
		}
		c.hub.At = hub

		// Its name goes on the side towards the router, which its nodes leave
		// clear, a little below the line in. The name runs away from the hub,
		// towards the router, away from the nodes of the hub.
		toRouter := math.Atan2(router.Y-hub.Y, router.X-hub.X)
		gap := hubLabelGap

		switch cos := math.Cos(toRouter); {
		case cos < -0.5:
			c.hub.Anchor = "end"
		case cos > 0.5:
			c.hub.Anchor = "start"
		default:
			// Across the branch, it is wider than the gap that the nodes
			// leave. So it starts after them, and grows away from the hub.
			c.hub.Anchor = "middle"
			gap = c.radius + labelGap

			c.hub.Hangs = "down"
			if math.Sin(toRouter) < 0 {
				c.hub.Hangs = "up"
			}
		}

		c.hub.LabelAt = polar(hub, gap, toRouter)
		if c.hub.Hangs == "" {
			c.hub.LabelAt.Y += 4
		}

		// The nodes go around the hub. They leave the side towards the router
		// for its branch.
		n := c.size()
		from := toRouter + routerGap/2
		step := (2*math.Pi - routerGap) / float64(max(n, 1))
		angle := func(i int) float64 { return from + step*(float64(i)+0.5) }

		for i, d := range c.devices {
			p := polar(hub, c.radius, angle(i))
			l.Devices = append(l.Devices, Device{
				MapDevice: d, At: p, Sector: c.hub.Index,
				Label: radialLabel(hub, angle(i), c.radius+labelGap, d.Name),
			})
			l.Lines = append(l.Lines, branch(hub, p, d.Bytes, nodeTop, d.Active, d.Name))
			c.hub.Active = c.hub.Active || d.Active
		}

		for i, o := range c.orgs {
			p := polar(hub, c.radius, angle(i))
			l.Orgs = append(l.Orgs, Org{
				MapOrg: o, At: p,
				Label: radialLabel(hub, angle(i), c.radius+labelGap, o.Short),
			})
			l.Lines = append(l.Lines, branch(hub, p, o.Bytes, nodeTop, o.Active, o.Short))
			c.hub.Active = c.hub.Active || o.Active
		}

		l.Lines = append(l.Lines, branch(router, hub, c.bytes, hubTop, c.hub.Active, c.hub.Label))
		l.Hubs = append(l.Hubs, c.hub)
	}

	// Quiet lines first. So the busy lines are drawn over them.
	slices.SortStableFunc(l.Lines, func(a, b Line) int { return cmp.Compare(a.Width, b.Width) })

	l.Links = links(m, l)

	return l
}

// links draws each link of m between the nodes that l placed. The links bend
// towards the router. So they stay clear of the clusters that they leave.
func links(m *inventory.TrafficMap, l *Layout) []Link {
	type end struct {
		at   Point
		name string
	}

	ends := make(map[string]end, len(l.Devices)+len(l.Orgs))
	for _, d := range l.Devices {
		ends[d.Key()] = end{d.At, d.Name}
	}

	for _, o := range l.Orgs {
		ends[o.Key()] = end{o.At, o.Short}
	}

	var top int64 = 1
	for _, k := range m.Links {
		top = max(top, k.Bytes)
	}

	var out []Link

	for _, k := range m.Links {
		a, b := DeviceKey(k.Device), DeviceKey(k.PeerDevice)
		if k.PeerDevice == 0 {
			b = OrgKey(k.PeerASN)
		}

		p, okA := ends[a]
		q, okB := ends[b]

		if !okA || !okB {
			continue
		}

		mid := Point{(p.at.X + q.at.X) / 2, (p.at.Y + q.at.Y) / 2}
		c := Point{round((mid.X + l.Router.X) / 2), round((mid.Y + l.Router.Y) / 2)}

		out = append(out, Link{
			Path:     fmt.Sprintf("M %g %g Q %g %g %g %g", p.at.X, p.at.Y, c.X, c.Y, q.at.X, q.at.Y),
			Width:    round(stroke(k.Bytes, top) * linkScale),
			Active:   k.Active,
			A:        a,
			B:        b,
			Title:    p.name + " ↔ " + q.name,
			LabelAt:  along(p.at, c, q.at, labelAlong),
			Services: k.Services,
		})
	}

	slices.SortStableFunc(out, func(a, b Link) int { return cmp.Compare(a.Width, b.Width) })

	return out
}

// buildClusters groups devices by segment, in the order of the segments, and
// each by id. They come after the organisations of the internet by ASN. It
// leaves out empty groups.
func buildClusters(m *inventory.TrafficMap, segments []Segment) []*cluster {
	var out []*cluster

	if len(m.Orgs) > 0 {
		c := &cluster{hub: Hub{Label: "Internet", Internet: true, Index: -1, Nodes: len(m.Orgs)}, orgs: slices.Clone(m.Orgs)}
		slices.SortFunc(c.orgs, func(a, b *inventory.MapOrg) int { return cmp.Compare(a.ASN, b.ASN) })

		for _, o := range c.orgs {
			c.bytes += o.Bytes
		}

		out = append(out, c)
	}

	index := make(map[int64]int, len(segments))
	groups := make([]*cluster, len(segments))

	for i, s := range segments {
		index[s.ID] = i
		groups[i] = &cluster{hub: Hub{Label: s.Label, Index: i}}
	}

	other, ok := index[0]
	if !ok {
		other = len(groups)
		groups = append(groups, &cluster{hub: Hub{Label: "Elsewhere", Index: other}})
	}

	for _, d := range m.Devices {
		i, ok := index[d.NetworkID]
		if !ok {
			i = other
		}

		groups[i].devices = append(groups[i].devices, d)
		groups[i].bytes += d.Bytes
	}

	for _, g := range groups {
		if len(g.devices) == 0 {
			continue
		}

		slices.SortFunc(g.devices, func(a, b *inventory.MapDevice) int { return cmp.Compare(a.ID, b.ID) })
		g.hub.Nodes = len(g.devices)
		out = append(out, g)
	}

	return out
}

// branch is a straight line from p to q for n bytes, against the busiest line
// of its kind.
func branch(p, q Point, n, top int64, active bool, title string) Line {
	return Line{
		Path:   fmt.Sprintf("M %g %g L %g %g", p.X, p.Y, q.X, q.Y),
		Width:  stroke(n, top),
		Active: active,
		Title:  title,
	}
}

// along is the point at part t of the curve from p to q, bent towards c.
func along(p, c, q Point, t float64) Point {
	a, b, d := (1-t)*(1-t), 2*t*(1-t), t*t

	return Point{X: round(a*p.X + b*c.X + d*q.X), Y: round(a*p.Y + b*c.Y + d*q.Y)}
}

// polar is the point at radius r and angle a from c, clockwise from the right,
// because the y of SVG runs down.
func polar(c Point, r, a float64) Point {
	return Point{X: round(c.X + r*math.Cos(a)), Y: round(c.Y + r*math.Sin(a))}
}

// radialLabel sets text along the radius from c at angle a, from r away from
// it. On the left half, it turns the text the other way. So the text is never
// upside down.
func radialLabel(c Point, a, r float64, text string) Label {
	deg := math.Mod(a*180/math.Pi+720, 360)
	l := Label{At: polar(c, r, a), Rotate: round(deg), Anchor: "start", Text: shorten(text)}

	if deg > 90 && deg < 270 {
		l.Rotate = round(deg - 180)
		l.Anchor = "end"
	}

	return l
}

// stroke is the width of a line for n bytes against the top of the busiest
// line, on a log scale. A line a thousand times busier is thicker, but by much
// less than a thousand times.
func stroke(n, top int64) float64 {
	if n <= 1 || top <= 1 {
		return minStroke
	}

	f := math.Log(float64(n)) / math.Log(float64(top))

	return round(minStroke + (maxStroke-minStroke)*min(max(f, 0), 1))
}

// shorten cuts a name that would go into the next cluster.
func shorten(s string) string {
	r := []rune(s)
	if len(r) <= maxLabel {
		return s
	}

	return string(r[:maxLabel-1]) + "…"
}

func round(f float64) float64 { return math.Round(f*10) / 10 }
