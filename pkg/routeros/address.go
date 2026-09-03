package routeros

import "context"

// IPAddress is one row of /ip/address: a prefix the router is a host on.
//
// Between them these rows are the segments the router serves, which is the
// only place the mapping from a prefix to the interface carrying it exists.
// Nothing on the wire carries it: a sweep sees addresses and never learns
// which VLAN they belong to.
type IPAddress struct {
	ID string `json:".id"`

	// Address is the router's own address with the prefix length, such as
	// "192.0.2.1/24", and Network is the prefix's base. The length lives only
	// on the first, so that is the field a caller parses.
	Address string `json:"address"`
	Network string `json:"network"`

	// Interface is the interface the address is configured on, and
	// ActualInterface the one it resolved to. They differ when the configured
	// name is a bridge port or a list, so a lookup that misses on the first
	// should try the second.
	Interface       string `json:"interface"`
	ActualInterface string `json:"actual-interface"`

	// Dynamic separates an address the router was handed from one an operator
	// configured. A WAN address from the ISP's DHCP server is dynamic, and it
	// is a link the router sits on rather than a segment it serves.
	Dynamic Bool `json:"dynamic"`

	// Invalid marks an address the router itself no longer believes -- its
	// interface is gone -- and Disabled one an operator turned off.
	Invalid  Bool `json:"invalid"`
	Disabled Bool `json:"disabled"`

	// Comment is the operator's note on the address, and is the closest thing
	// the router holds to a name a person chose for the segment.
	Comment string `json:"comment"`
}

// Usable reports whether the address describes a segment the router serves
// now: believed, switched on, and configured rather than handed to it.
func (a IPAddress) Usable() bool {
	return !bool(a.Invalid) && !bool(a.Disabled) && !bool(a.Dynamic)
}

// Addresses returns the prefixes the router is a host on.
func (r *RouterOS) Addresses(ctx context.Context) ([]IPAddress, error) {
	return list[IPAddress](ctx, r, addressAPI)
}
