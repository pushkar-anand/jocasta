package routeros

import "context"

// LeaseStatusBound is the lease status that means a client accepted the
// address and uses it. Every other status, such as "waiting", "offered" or
// "busy", describes the address. It tells nothing about a device.
const LeaseStatusBound = "bound"

// DHCPLease is one row of /ip/dhcp-server/lease.
//
// A lease is better than an ARP entry in one way, and worse in another. It has
// a name, which ARP never has. But it stays after the device goes. So a static
// lease can name a device that was disconnected a month ago. Both facts are
// important, so Status is here.
type DHCPLease struct {
	ID string `json:".id"`

	// Address is the leased address. ActiveAddress is the address in use now.
	// They are different while the client and server negotiate the lease
	// again. A static lease that no client uses has only Address.
	Address       string `json:"address"`
	ActiveAddress string `json:"active-address"`

	MACAddress       string `json:"mac-address"`
	ActiveMACAddress string `json:"active-mac-address"`

	// HostName is the name that the client gave itself in its DHCP request.
	// So it is the claim of the device about its name. It is often absent.
	HostName string `json:"host-name"`

	// Comment is the free-text note of the operator on the lease. It can be a
	// name, but it can also be "spare, do not reuse".
	Comment string `json:"comment"`

	// Server is the DHCP server instance. On a router with VLANs, it names
	// the segment of the device.
	Server string `json:"server"`

	// Status is the lease state. See [LeaseStatusBound].
	Status string `json:"status"`

	// Dynamic separates a lease that the server gave out from a lease that an
	// operator configured. A static lease is the intent of the operator. It
	// stays, also when no client uses it.
	Dynamic  Bool `json:"dynamic"`
	Disabled Bool `json:"disabled"`
	Blocked  Bool `json:"blocked"`

	// ExpiresAfter and LastSeen are RouterOS durations, such as "9m59s" or
	// "1w2d". They stay as text. The router calculates them from a clock that
	// this client cannot see. A conversion here would add a precision that the
	// answer does not have.
	ExpiresAfter string `json:"expires-after"`
	LastSeen     string `json:"last-seen"`

	ClientID string `json:"client-id"`
}

// Static reports whether an operator configured this lease.
func (l DHCPLease) Static() bool { return !bool(l.Dynamic) }

// Bound reports whether a client holds this address now. A static lease for a
// device that is not connected is not bound. So it does not count as a
// sighting.
func (l DHCPLease) Bound() bool { return l.Status == LeaseStatusBound }

// DHCPLeases returns every lease of the router: static and dynamic, bound and
// not bound.
func (r *RouterOS) DHCPLeases(ctx context.Context) ([]DHCPLease, error) {
	return list[DHCPLease](ctx, r, dhcpLeaseAPI)
}
