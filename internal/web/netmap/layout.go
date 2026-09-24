// Package netmap lays out the traffic map: where each device, organisation
// and line goes on the canvas.
//
// The map is a tree. The router is at the centre, with a hub for each network
// and one for the internet around it; each hub has its devices, or the
// internet's organisations, fanned around it. The lines are the branches, and
// a branch is thicker the more traffic passed along it. Over the tree, a link
// joins each device to whatever it exchanged traffic with -- an organisation,
// or a device on another network -- with the services it carried.
//
// The layout is a pure function of what it is given, ordered by id rather than
// by traffic, so a device keeps its place from one refresh to the next and the
// picture only changes where the network did.
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
	// nodeSpacing is the distance between neighbours around a hub, enough
	// for the labels that point away from it not to touch.
	nodeSpacing = 24.0

	// minCluster is the smallest radius a hub's nodes sit at.
	minCluster = 70.0

	// labelGap is how far a label starts beyond its node, and labelSpan the
	// room one takes, for keeping clusters apart.
	labelGap  = 12.0
	labelSpan = 170.0

	// routerGap is the angle a hub leaves empty on the side facing the
	// router, where its branch comes in.
	routerGap = 50 * math.Pi / 180

	// minHubRing is the smallest distance from the router to a hub, and
	// margin the empty edge around the whole map.
	minHubRing = 260.0
	margin     = 40.0

	// wide stretches the ring of hubs across, since the map is shown on a
	// screen wider than it is tall.
	wide = 1.4

	// hubLabelGap is how far a hub's name sits from it, towards the router,
	// in the gap its devices leave clear.
	hubLabelGap = 34.0

	maxLabel = 24

	minStroke = 0.75
	maxStroke = 6.0

	// labelAlong is how far along a link its services are named: towards
	// the peer, clear of the router the link bends past.
	labelAlong = 0.7

	// linkScale thins the links against the branches, so the tree still
	// reads under them.
	linkScale = 0.6
)

// Point is a position on the canvas.
type Point struct{ X, Y float64 }

// Segment is one network the devices are grouped by, in the order the map
// shows them. ID zero holds the devices on no recorded network.
type Segment struct {
	ID    int64
	Label string
}

// Label is text beside a node, turned to point away from its hub.
type Label struct {
	At     Point
	Rotate float64
	Anchor string // "start" or "end"
	Text   string
}

// Hub is a network's node, or the internet's, between the router and what
// hangs off it.
type Hub struct {
	Label    string
	At       Point
	LabelAt  Point
	Anchor   string // how its name is aligned on LabelAt
	Index    int    // which network, for its colour
	Internet bool
	Active   bool

	// Nodes counts the devices, or organisations, around it.
	Nodes int
}

// Device is a device placed around its network's hub.
type Device struct {
	*inventory.MapDevice

	At     Point
	Label  Label
	Sector int
}

// Org is an organisation placed around the internet's hub.
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

// Link is traffic between two nodes, drawn over the tree. A and B are the
// keys of its ends, as DeviceKey and OrgKey give them.
type Link struct {
	Path     string
	Width    float64
	Active   bool
	A, B     string
	Title    string
	LabelAt  Point
	Services []inventory.MapService
}

// Layout is everything the template draws.
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

// Key is the device's name for its links.
func (d Device) Key() string { return DeviceKey(d.ID) }

// Key is the organisation's name for its links.
func (o Org) Key() string { return OrgKey(o.ASN) }

// cluster is one hub with what hangs off it, before it is placed.
type cluster struct {
	hub     Hub
	devices []*inventory.MapDevice
	orgs    []*inventory.MapOrg
	bytes   int64

	// radius is where its nodes sit, and reach how far from the hub its
	// labels end.
	radius, reach float64

	angle float64
}

func (c *cluster) size() int { return len(c.devices) + len(c.orgs) }

// Place lays out m with its devices grouped into segments, in their order.
// A device whose network is not among them is grouped with segment zero.
func Place(m *inventory.TrafficMap, segments []Segment) *Layout {
	clusters := buildClusters(m, segments)
	if len(clusters) == 0 {
		return &Layout{Width: 2 * minHubRing, Height: 2 * minHubRing, Router: Point{minHubRing, minHubRing}}
	}

	// Each hub gets a share of the circle by how far its cluster reaches, and
	// the ring is as big as it has to be for neighbours not to meet. The
	// internet comes first, centred due right.
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

		// Its name goes on the side facing the router, which its nodes leave
		// clear, a little below the line in.
		toRouter := math.Atan2(router.Y-hub.Y, router.X-hub.X)
		c.hub.LabelAt = polar(hub, hubLabelGap, toRouter)
		c.hub.LabelAt.Y += 4

		// The name runs away from the hub, towards the router, rather than
		// back over the hub's own nodes.
		switch cos := math.Cos(toRouter); {
		case cos < -0.5:
			c.hub.Anchor = "end"
		case cos > 0.5:
			c.hub.Anchor = "start"
		default:
			c.hub.Anchor = "middle"
		}

		// The nodes go round the hub, leaving the side that faces the router
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

	// Quiet lines first, so the busy ones are drawn over them.
	slices.SortStableFunc(l.Lines, func(a, b Line) int { return cmp.Compare(a.Width, b.Width) })

	l.Links = links(m, l)

	return l
}

// links draws each of m's links between the nodes l placed, bending in
// towards the router so they clear the clusters they leave.
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

// buildClusters groups devices by segment, in the segments' order and each by
// id, after the internet's organisations by ASN. Empty groups are left out.
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

// branch is a straight line from p to q for n bytes, against the busiest
// line of its kind.
func branch(p, q Point, n, top int64, active bool, title string) Line {
	return Line{
		Path:   fmt.Sprintf("M %g %g L %g %g", p.X, p.Y, q.X, q.Y),
		Width:  stroke(n, top),
		Active: active,
		Title:  title,
	}
}

// along is the point t of the way along the curve from p to q bent towards c.
func along(p, c, q Point, t float64) Point {
	a, b, d := (1-t)*(1-t), 2*t*(1-t), t*t

	return Point{X: round(a*p.X + b*c.X + d*q.X), Y: round(a*p.Y + b*c.Y + d*q.Y)}
}

// polar is the point at radius r and angle a from c, clockwise from due right,
// as SVG's y runs down.
func polar(c Point, r, a float64) Point {
	return Point{X: round(c.X + r*math.Cos(a)), Y: round(c.Y + r*math.Sin(a))}
}

// radialLabel sets text along the radius from c at angle a, starting r from
// it; on the left half it is turned the other way round so it never reads
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

// stroke is a line's width for n bytes against the busiest line's top, on a
// log scale: a line a thousand times busier is thicker, not a thousand times.
func stroke(n, top int64) float64 {
	if n <= 1 || top <= 1 {
		return minStroke
	}

	f := math.Log(float64(n)) / math.Log(float64(top))

	return round(minStroke + (maxStroke-minStroke)*min(max(f, 0), 1))
}

// shorten cuts a name that would run into the next cluster.
func shorten(s string) string {
	r := []rune(s)
	if len(r) <= maxLabel {
		return s
	}

	return string(r[:maxLabel-1]) + "…"
}

func round(f float64) float64 { return math.Round(f*10) / 10 }
