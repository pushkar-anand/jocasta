package routeros

import (
	"cmp"
	"context"
	"strings"
)

// Neighbor is one row of /ip/neighbor. It is a device that announced itself on
// an interface of the router, over MNDP, LLDP or CDP. Switches and access
// points announce themselves. Most end devices do not.
type Neighbor struct {
	ID string `json:".id"`

	// Interface is every interface that heard the announcement,
	// comma-separated, such as "ether3,bridge". See [Neighbor.Port].
	Interface string `json:"interface"`

	MACAddress string `json:"mac-address"`

	// Address is the management address that the neighbour advertised.
	// RouterOS 7 also splits it into Address4 and Address6.
	Address  string `json:"address"`
	Address4 string `json:"address4"`

	// Identity is the name that the neighbour gives itself. Platform is the
	// software of its maker ("MikroTik"). Board is its model.
	Identity string `json:"identity"`
	Platform string `json:"platform"`
	Board    string `json:"board"`
	Version  string `json:"version"`

	// InterfaceName is the name that the neighbour gives the port that sent
	// the announcement. LLDP and MNDP both carry it.
	InterfaceName string `json:"interface-name"`

	// SystemCapsEnabled is what the neighbour says that it does, such as
	// "bridge,router" or "wlan-ap".
	SystemCapsEnabled string `json:"system-caps-enabled"`

	DiscoveredBy string `json:"discovered-by"`
}

// Port returns the first interface in the interface list of the neighbour. If
// a VLAN interface heard the announcement, this is the VLAN interface.
func (n Neighbor) Port() string {
	port, _, _ := strings.Cut(n.Interface, ",")

	return strings.TrimSpace(port)
}

// Addr returns the IPv4 management address that the neighbour advertised. If
// there is none, it returns the single address that the router shows.
func (n Neighbor) Addr() string {
	return cmp.Or(n.Address4, n.Address)
}

// Neighbors returns the devices that announced themselves to the router.
func (r *RouterOS) Neighbors(ctx context.Context) ([]Neighbor, error) {
	return list[Neighbor](ctx, r, neighborAPI)
}
