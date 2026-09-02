package routeros

import "context"

// LeaseStatusBound is the lease status meaning a client accepted the address
// and is using it. Every other status -- "waiting", "offered", "busy" -- is
// the router talking about an address rather than about a device.
const LeaseStatusBound = "bound"

// DHCPLease is one row of /ip/dhcp-server/lease.
//
// A lease is worth more than an ARP entry in one respect and less in another:
// it carries a name, which ARP never does, and it outlives the device's
// presence, so a static lease can name something that was unplugged a month
// ago. Both matter, which is why Status is here.
type DHCPLease struct {
	ID string `json:".id"`

	// Address is the leased address; ActiveAddress is the one currently in
	// use, and they differ while a lease is being renegotiated. Static leases
	// that no client is using carry the first and not the second.
	Address       string `json:"address"`
	ActiveAddress string `json:"active-address"`

	MACAddress       string `json:"mac-address"`
	ActiveMACAddress string `json:"active-mac-address"`

	// HostName is what the client called itself in its DHCP request, so it is
	// the device's own claim about its name and is often absent.
	HostName string `json:"host-name"`

	// Comment is the operator's free-text note on the lease, and is a note and
	// not a name: "spare, do not reuse" is as likely as anything hostname-
	// shaped. What a caller does with it is the caller's business.
	Comment string `json:"comment"`

	// Server is the DHCP server instance, which on a VLAN'd router names the
	// segment the device is on.
	Server string `json:"server"`

	// Status is the lease state; see [LeaseStatusBound].
	Status string `json:"status"`

	// Dynamic separates a lease the server handed out from one an operator
	// configured. A static lease is the operator's intent and stands whether
	// or not anyone is using it.
	Dynamic  Bool `json:"dynamic"`
	Disabled Bool `json:"disabled"`
	Blocked  Bool `json:"blocked"`

	// ExpiresAfter and LastSeen are RouterOS durations such as "9m59s" or
	// "1w2d", left as text. They are the router's own arithmetic against a
	// clock this client cannot see, so converting them here would invent a
	// precision the answer does not have.
	ExpiresAfter string `json:"expires-after"`
	LastSeen     string `json:"last-seen"`

	ClientID string `json:"client-id"`
}

// Static reports whether an operator configured this lease rather than the
// server handing it out.
func (l DHCPLease) Static() bool { return !bool(l.Dynamic) }

// Bound reports whether a client is holding this address now. A static lease
// for a device nobody has plugged in is not bound, which is what keeps it from
// reading as a sighting.
func (l DHCPLease) Bound() bool { return l.Status == LeaseStatusBound }

// DHCPLeases returns every lease the router holds, static and dynamic, bound
// and not.
func (r *RouterOS) DHCPLeases(ctx context.Context) ([]DHCPLease, error) {
	return list[DHCPLease](ctx, r, dhcpLeaseAPI)
}
