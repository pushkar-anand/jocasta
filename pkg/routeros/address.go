package routeros

import "context"

// IPAddress is one row of /ip/address: a prefix on which the router is a
// host.
//
// Together, these rows are the segments that the router serves. Only this
// table maps a prefix to the interface that carries it. No packet carries this
// mapping. A sweep sees addresses, but never learns their VLAN.
type IPAddress struct {
	ID string `json:".id"`

	// Address is the own address of the router with the prefix length, such
	// as "192.0.2.1/24". Network is the base of the prefix. Only Address has
	// the length, so a caller parses Address.
	Address string `json:"address"`
	Network string `json:"network"`

	// Interface is the interface on which the address is configured.
	// ActualInterface is the interface that it resolved to. They are different
	// when the configured name is a bridge port or a list. So if a lookup
	// misses on Interface, try ActualInterface.
	Interface       string `json:"interface"`
	ActualInterface string `json:"actual-interface"`

	// Dynamic separates an address that the router got from an address that
	// an operator configured. A WAN address from the DHCP server of the ISP is
	// dynamic. The router is on that link, but does not serve it as a
	// segment.
	Dynamic Bool `json:"dynamic"`

	// Invalid marks an address that the router no longer trusts, because its
	// interface is gone. Disabled marks an address that an operator turned
	// off.
	Invalid  Bool `json:"invalid"`
	Disabled Bool `json:"disabled"`

	// Comment is the note of the operator on the address. It is the nearest
	// thing that the router has to a name that a person chose for the
	// segment.
	Comment string `json:"comment"`
}

// Usable reports whether the address is a segment that the router serves now.
// The address must be valid, enabled, and configured by an operator.
func (a IPAddress) Usable() bool {
	return !bool(a.Invalid) && !bool(a.Disabled) && !bool(a.Dynamic)
}

// Addresses returns the prefixes on which the router is a host.
func (r *RouterOS) Addresses(ctx context.Context) ([]IPAddress, error) {
	return list[IPAddress](ctx, r, addressAPI)
}
