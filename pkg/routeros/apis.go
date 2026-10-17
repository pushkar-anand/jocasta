package routeros

// The endpoints that this client reads. Each is the console path with /rest
// in front. This is the full addressing scheme of the REST API.
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

	// ethernetMonitorAPI is a command. The REST API runs it on a POST.
	ethernetMonitorAPI = "/interface/ethernet/monitor"

	// RouterOS 7.13 replaced the wireless package with wifi. A router has one
	// of them, or neither.
	wifiRegistrationAPI     = "/interface/wifi/registration-table"
	wirelessRegistrationAPI = "/interface/wireless/registration-table"
	wirelessAPI             = "/interface/wireless"
)
