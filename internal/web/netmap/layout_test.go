package netmap

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

func manyDevices(n int, networks ...int64) *inventory.TrafficMap {
	m := &inventory.TrafficMap{}

	for i := range n {
		m.Devices = append(m.Devices, &inventory.MapDevice{
			ID: int64(i + 1), Name: fmt.Sprintf("host-%d", i+1),
			NetworkID: networks[i%len(networks)], Bytes: int64(1000 * (n - i)),
		})
	}

	return m
}

func withOrgs(m *inventory.TrafficMap, n int) *inventory.TrafficMap {
	for i := range n {
		m.Orgs = append(m.Orgs, &inventory.MapOrg{ASN: uint32(64500 + i), Short: fmt.Sprintf("Org %d", i), Bytes: 500})
	}

	return m
}

// A device keeps its place however its traffic changes, since places follow
// ids.
func TestPlaceIsStable(t *testing.T) {
	t.Parallel()

	segments := []Segment{{ID: 1, Label: "lan"}, {ID: 2, Label: "iot"}}

	first := Place(manyDevices(10, 1, 2), segments)

	busier := manyDevices(10, 1, 2)
	for _, d := range busier.Devices {
		d.Bytes = 1000 * d.ID
	}

	second := Place(busier, segments)

	at := map[int64]Point{}
	for _, d := range first.Devices {
		at[d.ID] = d.At
	}

	for _, d := range second.Devices {
		assert.Equal(t, at[d.ID], d.At, "device %d moved", d.ID)
	}
}

// At the most the map draws, however they are split, no node sits on another
// and everything is on the canvas.
func TestPlaceKeepsNodesApart(t *testing.T) {
	t.Parallel()

	for _, networks := range [][]int64{{1}, {1, 2, 3, 0}, {1, 2, 3, 4, 5, 6}} {
		m := withOrgs(manyDevices(inventory.MapMaxDevices, networks...), inventory.MapMaxOrgs)
		l := Place(m, []Segment{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}, {ID: 6}})

		var points []Point
		for _, d := range l.Devices {
			points = append(points, d.At)
		}

		for _, o := range l.Orgs {
			points = append(points, o.At)
		}

		for _, h := range l.Hubs {
			points = append(points, h.At)
		}

		points = append(points, l.Router)

		for i, a := range points {
			for _, b := range points[i+1:] {
				assert.Greater(t, math.Hypot(a.X-b.X, a.Y-b.Y), 14.0, "%v and %v overlap with %d networks", a, b, len(networks))
			}

			assert.Positive(t, a.X)
			assert.Positive(t, a.Y)
			assert.Less(t, a.X, l.Width)
			assert.Less(t, a.Y, l.Height)
		}
	}
}

// The internet's hub comes first, then the networks in the order given with
// devices on none last; a network with no devices has no hub.
func TestPlaceHubsInOrder(t *testing.T) {
	t.Parallel()

	m := withOrgs(&inventory.TrafficMap{Devices: []*inventory.MapDevice{
		{ID: 3, NetworkID: 2}, {ID: 1, NetworkID: 9}, {ID: 2, NetworkID: 1},
	}}, 2)

	l := Place(m, []Segment{{ID: 1, Label: "lan"}, {ID: 5, Label: "empty"}, {ID: 2, Label: "iot"}})

	var labels []string
	for _, h := range l.Hubs {
		labels = append(labels, h.Label)
	}

	assert.Equal(t, []string{"Internet", "lan", "iot", "Elsewhere"}, labels)
	assert.Equal(t, 2, l.Hubs[0].Nodes)
	assert.Equal(t, 1, l.Hubs[1].Nodes)
	assert.Less(t, l.Router.Y-2, l.Hubs[0].At.Y, "the internet is due right of the router")
	assert.Greater(t, l.Hubs[0].At.X, l.Router.X)
	assert.Less(t, l.Hubs[0].LabelAt.X, l.Hubs[0].At.X, "its name faces the router")
	assert.Equal(t, "end", l.Hubs[0].Anchor, "and ends short of the hub")

	var ids []int64
	for _, d := range l.Devices {
		ids = append(ids, d.ID)
	}

	assert.Equal(t, []int64{2, 3, 1}, ids)
}

// The lines are the tree's branches: one to each node, one to each hub. An
// active node lights its branch and its hub's; busier branches are thicker and
// drawn last.
func TestPlaceDrawsTheTree(t *testing.T) {
	t.Parallel()

	m := &inventory.TrafficMap{
		Devices: []*inventory.MapDevice{
			{ID: 1, Name: "host-a", Bytes: 1_000_000, Active: true},
			{ID: 2, Name: "host-b", Bytes: 1_000},
		},
		Orgs: []*inventory.MapOrg{{ASN: 64500, Short: "Example", Bytes: 5_000}},
	}

	l := Place(m, nil)
	require.Len(t, l.Lines, 5, "two devices, one organisation, two hubs")

	byTitle := map[string]Line{}
	for _, ln := range l.Lines {
		byTitle[ln.Title] = ln
	}

	assert.True(t, byTitle["host-a"].Active)
	assert.False(t, byTitle["host-b"].Active)
	assert.True(t, byTitle["Elsewhere"].Active, "its hub carries the active device")
	assert.False(t, byTitle["Internet"].Active)
	assert.Equal(t, maxStroke, byTitle["host-a"].Width)
	assert.Less(t, byTitle["host-b"].Width, byTitle["host-a"].Width)
	assert.Equal(t, maxStroke, l.Lines[len(l.Lines)-1].Width, "the busiest is drawn last")
}

func TestPlaceWithNothing(t *testing.T) {
	t.Parallel()

	l := Place(&inventory.TrafficMap{}, nil)
	assert.Empty(t, l.Hubs)
	assert.Positive(t, l.Width)
}

// A label on the left of its hub is turned round to read left to right.
func TestLabelsNeverReadUpsideDown(t *testing.T) {
	t.Parallel()

	right := radialLabel(Point{}, 0.1, 100, "x")
	left := radialLabel(Point{}, math.Pi, 100, "x")

	assert.Equal(t, "start", right.Anchor)
	assert.Equal(t, "end", left.Anchor)
	assert.InDelta(t, 0, left.Rotate, 0.1)
}

func TestShortenCutsLongNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "host-a", shorten("host-a"))
	assert.Equal(t, maxLabel, len([]rune(shorten("a-very-long-device-name.example.com"))))
}

// Every link joins the two nodes it names (a device and an organisation, or
// devices on two networks), keeping its services, and one to a node the map
// left off is not drawn.
func TestPlaceDrawsLinksBetweenNodes(t *testing.T) {
	t.Parallel()

	https := []inventory.MapService{{Protocol: 6, Port: 443, Name: "https"}}
	m := &inventory.TrafficMap{
		Devices: []*inventory.MapDevice{
			{ID: 1, Name: "host-a", NetworkID: 10},
			{ID: 2, Name: "host-b", NetworkID: 50},
		},
		Orgs: []*inventory.MapOrg{{ASN: 64500, Short: "Example"}},
		Links: []*inventory.MapLink{
			{Device: 1, PeerASN: 64500, Bytes: 5_000, Active: true, Services: https},
			{Device: 1, PeerDevice: 2, Bytes: 1_000},
			{Device: 2, PeerDevice: 9, Bytes: 1_000},
		},
	}

	l := Place(m, []Segment{{ID: 10, Label: "lan"}, {ID: 50, Label: "iot"}})
	require.Len(t, l.Links, 2)

	byTitle := map[string]Link{}
	for _, k := range l.Links {
		byTitle[k.Title] = k
	}

	org := byTitle["host-a ↔ Example"]
	assert.Equal(t, "d1", org.A)
	assert.Equal(t, "o64500", org.B)
	assert.True(t, org.Active)
	assert.Equal(t, https, org.Services)

	lan := byTitle["host-a ↔ host-b"]
	assert.Equal(t, "d2", lan.B)
	assert.Less(t, lan.Width, org.Width)
	assert.LessOrEqual(t, org.Width, maxStroke*linkScale)
}

func TestAlongFollowsTheCurve(t *testing.T) {
	t.Parallel()

	p, c, q := Point{0, 0}, Point{50, 100}, Point{100, 0}
	assert.Equal(t, p, along(p, c, q, 0))
	assert.Equal(t, q, along(p, c, q, 1))
	assert.Equal(t, Point{50, 50}, along(p, c, q, 0.5))
}
