package routeros

import "context"

// Interface is one row of /interface: a port, a bridge, a VLAN, a radio, or a
// tunnel, told apart by Type.
type Interface struct {
	ID   string `json:".id"`
	Name string `json:"name"`

	// Type is what the router calls the kind of interface: "ether", "bridge",
	// "vlan", "wifi", "wlan", "pppoe-out" and so on.
	Type string `json:"type"`

	// MACAddress is the interface's own hardware address, empty on one that
	// has none, such as a tunnel.
	MACAddress string `json:"mac-address"`

	Running  Bool `json:"running"`
	Disabled Bool `json:"disabled"`

	Comment string `json:"comment"`
}

// Interfaces returns every interface the router has.
func (r *RouterOS) Interfaces(ctx context.Context) ([]Interface, error) {
	return list[Interface](ctx, r, interfaceAPI)
}
