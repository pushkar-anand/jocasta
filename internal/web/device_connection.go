package web

import (
	"context"

	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/topology"
	"github.com/pushkar-anand/jocasta/internal/web/netmap"
	"github.com/pushkar-anand/jocasta/internal/web/topomap"
)

// connection is where a device sits in the network's tree, for the device
// page: the path down from the router, and how the device hangs from the end
// of it.
type connection struct {
	// Placed is whether any source has seen the device on a port. The rest
	// is empty when it is not.
	Placed bool

	// Hops runs from the router down to what the device hangs from.
	Hops []connectionHop

	// Last is how the device hangs from the last hop: its port, or its Wi-Fi
	// network and band, and its VLAN.
	Last string

	// Current is whether the latest reads saw the device there. A device
	// placed from an older read is where it was last seen.
	Current bool

	// Focus is the key the topology page opens on.
	Focus string
}

// connectionHop is one router, switch or access point on the path.
type connectionHop struct {
	Name     string
	DeviceID int64

	// Via is how the hop hangs from the one before it, empty on the router.
	Via string
}

// deviceConnection returns where device id sits in the tree. It returns nil
// when no source has been read, so the page leaves the section out.
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

	return c, nil
}

// hops names each node on a path and how it hangs from the one before.
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
