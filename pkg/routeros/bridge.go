package routeros

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
)

// maxVLAN is the highest 802.1Q tag. VLANs cuts a range at this tag. So a
// mistyped row cannot make a caller allocate millions of tags.
const maxVLAN = 4094

// BridgePort is one row of /interface/bridge/port. It is an interface that is
// a member of a bridge, with the VLAN of its untagged traffic.
type BridgePort struct {
	ID        string `json:".id"`
	Interface string `json:"interface"`
	Bridge    string `json:"bridge"`

	// PVID is the VLAN for untagged frames that arrive on the port. It has a
	// meaning only when the bridge filters VLANs. See [BridgePort.VLAN].
	PVID string `json:"pvid"`

	Disabled Bool `json:"disabled"`

	// Inactive marks a port on which the bridge does not forward. Usually,
	// the port has no link.
	Inactive Bool `json:"inactive"`

	Comment string `json:"comment"`
}

// VLAN returns the PVID of the port as a number. It returns false when the
// router rendered something that is not a number.
func (p BridgePort) VLAN() (int, bool) {
	n, err := strconv.Atoi(p.PVID)
	if err != nil {
		return 0, false
	}

	return n, true
}

// BridgePorts returns every bridge membership of the router.
func (r *RouterOS) BridgePorts(ctx context.Context) ([]BridgePort, error) {
	return list[BridgePort](ctx, r, bridgePortAPI)
}

// BridgeVLAN is one row of /interface/bridge/vlan. It is a set of VLANs, with
// the ports that carry them tagged or untagged.
type BridgeVLAN struct {
	ID     string `json:".id"`
	Bridge string `json:"bridge"`

	// VLANIDs is the tags that this row covers, as the router renders them:
	// "10", "10,20" or "100-110". See [BridgeVLAN.VLANs].
	VLANIDs string `json:"vlan-ids"`

	// Tagged and Untagged are what an operator configured. The current-*
	// columns add what the router joined itself, such as a Wi-Fi interface
	// that a datapath put in a VLAN. Trust the current-* columns.
	Tagged          string `json:"tagged"`
	Untagged        string `json:"untagged"`
	CurrentTagged   string `json:"current-tagged"`
	CurrentUntagged string `json:"current-untagged"`

	Disabled Bool `json:"disabled"`
	Dynamic  Bool `json:"dynamic"`
}

// VLANs returns the tags that the row covers, in order, with ranges expanded.
// It skips a part that is not a tag.
func (v BridgeVLAN) VLANs() []int {
	var out []int

	for part := range strings.SplitSeq(v.VLANIDs, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")

		first, err := strconv.Atoi(lo)
		if err != nil || first < 1 || first > maxVLAN {
			continue
		}

		last := first

		if isRange {
			last, err = strconv.Atoi(hi)
			if err != nil || last < first {
				continue
			}

			last = min(last, maxVLAN)
		}

		for tag := first; tag <= last; tag++ {
			out = append(out, tag)
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// TaggedPorts returns the interfaces that carry these VLANs tagged. It
// prefers what the router does now to what the operator configured.
func (v BridgeVLAN) TaggedPorts() []string {
	return names(cmp.Or(v.CurrentTagged, v.Tagged))
}

// UntaggedPorts returns the interfaces that carry these VLANs untagged. It
// works as [BridgeVLAN.TaggedPorts] does.
func (v BridgeVLAN) UntaggedPorts() []string {
	return names(cmp.Or(v.CurrentUntagged, v.Untagged))
}

// names splits a comma-separated list of interface names. It removes blanks.
func names(s string) []string {
	var out []string

	for n := range strings.SplitSeq(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}

	return out
}

// BridgeVLANs returns the bridge VLAN table.
func (r *RouterOS) BridgeVLANs(ctx context.Context) ([]BridgeVLAN, error) {
	return list[BridgeVLAN](ctx, r, bridgeVLANAPI)
}

// BridgeHost is one row of /interface/bridge/host. It is a hardware address
// that the bridge learned, with the port on which it learned it. This table
// tells which port a device is behind.
type BridgeHost struct {
	ID         string `json:".id"`
	MACAddress string `json:"mac-address"`
	Bridge     string `json:"bridge"`

	// OnInterface is the port on which the bridge learned the address. Some
	// releases name it Interface. See [BridgeHost.Port].
	OnInterface string `json:"on-interface"`
	Interface   string `json:"interface"`

	// VID is the VLAN in which the bridge learned the address. It is empty,
	// unless the bridge filters VLANs.
	VID string `json:"vid"`

	// Local marks an own address of the router. External marks an address
	// that the switch chip reported.
	Local    Bool `json:"local"`
	External Bool `json:"external"`
	Dynamic  Bool `json:"dynamic"`
	Invalid  Bool `json:"invalid"`
	Disabled Bool `json:"disabled"`
}

// Port returns the interface on which the bridge learned the address.
func (h BridgeHost) Port() string {
	return cmp.Or(h.OnInterface, h.Interface)
}

// VLAN returns the VLAN in which the bridge learned the address. It returns
// false when the bridge did not tell.
func (h BridgeHost) VLAN() (int, bool) {
	n, err := strconv.Atoi(h.VID)
	if err != nil || n < 1 {
		return 0, false
	}

	return n, true
}

// BridgeHosts returns the hardware addresses that the bridge learned.
func (r *RouterOS) BridgeHosts(ctx context.Context) ([]BridgeHost, error) {
	return list[BridgeHost](ctx, r, bridgeHostAPI)
}
