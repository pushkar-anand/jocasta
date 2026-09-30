package routeros

import (
	"cmp"
	"context"
	"strings"
)

// Neighbor is one row of /ip/neighbor: a device that announced itself on one
// of the router's interfaces over MNDP, LLDP or CDP. Switches and access
// points announce themselves; most end devices do not.
type Neighbor struct {
	ID string `json:".id"`

	// Interface is every interface the announcement was heard on,
	// comma-separated, such as "ether3,bridge". See [Neighbor.Port].
	Interface string `json:"interface"`

	MACAddress string `json:"mac-address"`

	// Address is the management address the neighbour advertised. RouterOS 7
	// splits it into Address4 and Address6 as well.
	Address  string `json:"address"`
	Address4 string `json:"address4"`

	// Identity is the neighbour's own name for itself, Platform its maker's
	// software ("MikroTik") and Board its model.
	Identity string `json:"identity"`
	Platform string `json:"platform"`
	Board    string `json:"board"`
	Version  string `json:"version"`

	// InterfaceName is the neighbour's own name for the port that sent the
	// announcement, which LLDP carries and MNDP carries as well.
	InterfaceName string `json:"interface-name"`

	// SystemCapsEnabled is what the neighbour says it is doing, such as
	// "bridge,router" or "wlan-ap".
	SystemCapsEnabled string `json:"system-caps-enabled"`

	DiscoveredBy string `json:"discovered-by"`
}

// Port returns the first interface in the neighbour's interface list. For an
// announcement heard on a VLAN interface, that is the VLAN interface.
func (n Neighbor) Port() string {
	port, _, _ := strings.Cut(n.Interface, ",")

	return strings.TrimSpace(port)
}

// Addr returns the IPv4 management address the neighbour advertised, falling
// back to whatever single address the router shows.
func (n Neighbor) Addr() string {
	return cmp.Or(n.Address4, n.Address)
}

// Neighbors returns the devices that announced themselves to the router.
func (r *RouterOS) Neighbors(ctx context.Context) ([]Neighbor, error) {
	return list[Neighbor](ctx, r, neighborAPI)
}
