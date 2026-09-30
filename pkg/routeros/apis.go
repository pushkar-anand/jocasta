package routeros

// The endpoints this client reads. Each mirrors the console path with /rest
// prefixed, which is the whole of the REST API's addressing scheme.
const (
	resourceAPI  = "/system/resource"
	identityAPI  = "/system/identity"
	arpAPI       = "/ip/arp"
	dhcpLeaseAPI = "/ip/dhcp-server/lease"
	addressAPI   = "/ip/address"
	neighborAPI  = "/ip/neighbor"
	interfaceAPI = "/interface"
	vlanAPI      = "/interface/vlan"

	bridgePortAPI = "/interface/bridge/port"
	bridgeVLANAPI = "/interface/bridge/vlan"
	bridgeHostAPI = "/interface/bridge/host"

	// RouterOS 7.13 replaced the wireless package with wifi. A router carries
	// one or the other, or neither.
	wifiRegistrationAPI     = "/interface/wifi/registration-table"
	wirelessRegistrationAPI = "/interface/wireless/registration-table"
	wirelessAPI             = "/interface/wireless"
)
