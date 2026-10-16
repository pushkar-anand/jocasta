package web

import (
	"cmp"
	"context"

	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/topology"
	"github.com/pushkar-anand/jocasta/internal/web/netmap"
	"github.com/pushkar-anand/jocasta/internal/web/topomap"
)

// connection is where a device is in the tree of the network, for the device
// page: the path down from the router, and how the device hangs from its
// end.
type connection struct {
	// Placed tells whether a source saw the device on a port. If not, the rest
	// is empty.
	Placed bool

	// Hops goes from the router down to what the device hangs from.
	Hops []connectionHop

	// Last is how the device hangs from the last hop: its port, or its Wi-Fi
	// network and band, and its VLAN.
	Last string

	// Current tells whether the latest reads saw the device there. A device
	// placed from an older read is where a source last saw it.
	Current bool

	// Speed is how fast the own connection of the device runs. It is empty
	// when that is unknown, or when the connection belongs to a different
	// device. Capable is set only on a wired link that came up below the rate
	// that both ends can run at. It is that rate.
	Speed   string
	Capable string

	// Focus is the key on which the topology page opens.
	Focus string
}

// connectionHop is one router, switch or access point on the path.
type connectionHop struct {
	Name     string
	DeviceID int64

	// Via is how the hop hangs from the hop before it. It is empty on the
	// router.
	Via string
}

// deviceConnection returns where device id is in the tree. It returns nil when
// no source was read. So the page leaves out the section.
func deviceConnection(ctx context.Context, store *inventory.Store, id int64) (*connection, error) {
	tree, err := store.Topology(ctx)
	if err != nil {
		return nil, err
	}

	if tree.Root == nil {
		return nil, nil //nolint:nilnil // no tree is an answer: the page leaves the section out.
	}

	c := &connection{Focus: netmap.DeviceKey(id)}

	if node, ok := tree.Node(id); ok {
		path := topology.Path(node)

		c.Placed, c.Current = true, true
		c.Hops = hops(path[:len(path)-1])

		if node.Uplink != nil {
			c.Last = topomap.LinkLabel(node.Uplink)
			c.setSpeed(node.Uplink.Speed, topology.Radio{})
		}

		return c, nil
	}

	leaf, ok := tree.Leaf(id)
	if !ok {
		return c, nil
	}

	c.Placed, c.Current = true, leaf.Current
	c.Hops = hops(topology.Path(leaf.Owner))
	c.Last = topomap.Where(leaf)
	c.setSpeed(leaf.Speed, leaf.Radio)

	return c, nil
}

// setSpeed sets Speed from the wired link s of the device, or its Wi-Fi
// connection r. It sets Capable when s came up below the rate that both ends
// can run at.
func (c *connection) setSpeed(s topology.Speed, r topology.Radio) {
	c.Speed = cmp.Or(topomap.SpeedLabel(s), topomap.RadioLabel(r))

	if s.Slow() {
		c.Capable = topomap.Rate(s.Capable)
	}
}

// hops names each node on a path, and how it hangs from the node before.
func hops(path []*topology.Node) []connectionHop {
	out := make([]connectionHop, len(path))

	for i, n := range path {
		out[i] = connectionHop{Name: topomap.NodeName(n), DeviceID: n.DeviceID}

		if n.Uplink != nil {
			out[i].Via = topomap.LinkLabel(n.Uplink)
		}
	}

	return out
}
