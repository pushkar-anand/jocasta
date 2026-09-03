package routeros

// The endpoints this client reads. Each mirrors the console path with /rest
// prefixed, which is the whole of the REST API's addressing scheme.
const (
	resourceAPI  = "/system/resource"
	arpAPI       = "/ip/arp"
	dhcpLeaseAPI = "/ip/dhcp-server/lease"
	addressAPI   = "/ip/address"
	vlanAPI      = "/interface/vlan"
)
