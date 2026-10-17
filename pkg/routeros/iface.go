package routeros

import "context"

// Interface is one row of /interface: a port, a bridge, a VLAN, a radio, or a
// tunnel. Type tells which.
type Interface struct {
	ID   string `json:".id"`
	Name string `json:"name"`

	// Type is the name of the kind of interface in the router: "ether",
	// "bridge", "vlan", "wifi", "wlan", "pppoe-out" and others.
	Type string `json:"type"`

	// MACAddress is the hardware address of the interface. It is empty for an
	// interface that has none, such as a tunnel.
	MACAddress string `json:"mac-address"`

	Running  Bool `json:"running"`
	Disabled Bool `json:"disabled"`

	Comment string `json:"comment"`
}

// Interfaces returns every interface of the router.
func (r *RouterOS) Interfaces(ctx context.Context) ([]Interface, error) {
	return list[Interface](ctx, r, interfaceAPI)
}
