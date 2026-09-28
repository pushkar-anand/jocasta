package routeros

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
)

// maxVLAN is the highest 802.1Q tag. A range reaching past it is cut there, so
// a mistyped row cannot make a caller allocate millions of tags.
const maxVLAN = 4094

// BridgePort is one row of /interface/bridge/port: an interface that is a
// member of a bridge, and the VLAN its untagged traffic belongs to.
type BridgePort struct {
	ID        string `json:".id"`
	Interface string `json:"interface"`
	Bridge    string `json:"bridge"`

	// PVID is the VLAN untagged frames arriving on the port are placed in.
	// It means something only when the bridge filters VLANs; see
	// [BridgePort.VLAN].
	PVID string `json:"pvid"`

	Disabled Bool `json:"disabled"`

	// Inactive marks a port the bridge is not forwarding on, most often one
	// with no link.
	Inactive Bool `json:"inactive"`

	Comment string `json:"comment"`
}

// VLAN returns the port's PVID as a number, and false when the router rendered
// something that is not one.
func (p BridgePort) VLAN() (int, bool) {
	n, err := strconv.Atoi(p.PVID)
	if err != nil {
		return 0, false
	}

	return n, true
}

// BridgePorts returns every bridge membership the router has.
func (r *RouterOS) BridgePorts(ctx context.Context) ([]BridgePort, error) {
	return list[BridgePort](ctx, r, bridgePortAPI)
}

// BridgeVLAN is one row of /interface/bridge/vlan: a set of VLANs and the
// ports that carry them tagged or untagged.
type BridgeVLAN struct {
	ID     string `json:".id"`
	Bridge string `json:"bridge"`

	// VLANIDs is the tags this row covers, as the router renders them: "10",
	// "10,20" or "100-110". See [BridgeVLAN.VLANs].
	VLANIDs string `json:"vlan-ids"`

	// Tagged and Untagged are what an operator configured. The current-*
	// columns add what the router joined on its own, such as a Wi-Fi
	// interface a datapath placed in a VLAN, and are the ones to believe.
	Tagged          string `json:"tagged"`
	Untagged        string `json:"untagged"`
	CurrentTagged   string `json:"current-tagged"`
	CurrentUntagged string `json:"current-untagged"`

	Disabled Bool `json:"disabled"`
	Dynamic  Bool `json:"dynamic"`
}

// VLANs returns the tags the row covers, in order, with ranges expanded. A
// part that does not read as a tag is skipped.
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

// TaggedPorts returns the interfaces carrying these VLANs tagged, preferring
// what the router is doing now over what was configured.
func (v BridgeVLAN) TaggedPorts() []string {
	return names(cmp.Or(v.CurrentTagged, v.Tagged))
}

// UntaggedPorts returns the interfaces carrying these VLANs untagged, on the
// same terms as [BridgeVLAN.TaggedPorts].
func (v BridgeVLAN) UntaggedPorts() []string {
	return names(cmp.Or(v.CurrentUntagged, v.Untagged))
}

// names splits a comma-separated list of interface names, dropping blanks.
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

// BridgeHost is one row of /interface/bridge/host: a hardware address the
// bridge learned, and the port it learned it on. This is the table that says
// which port a device is behind.
type BridgeHost struct {
	ID         string `json:".id"`
	MACAddress string `json:"mac-address"`
	Bridge     string `json:"bridge"`

	// OnInterface is the port the address was learned on. Some releases name
	// it Interface instead; see [BridgeHost.Port].
	OnInterface string `json:"on-interface"`
	Interface   string `json:"interface"`

	// VID is the VLAN the address was learned in, empty unless the bridge
	// filters VLANs.
	VID string `json:"vid"`

	// Local marks one of the router's own addresses, and External one the
	// switch chip reported.
	Local    Bool `json:"local"`
	External Bool `json:"external"`
	Dynamic  Bool `json:"dynamic"`
	Invalid  Bool `json:"invalid"`
	Disabled Bool `json:"disabled"`
}

// Port returns the interface the address was learned on.
func (h BridgeHost) Port() string {
	return cmp.Or(h.OnInterface, h.Interface)
}

// VLAN returns the VLAN the address was learned in, and false when the bridge
// did not say.
func (h BridgeHost) VLAN() (int, bool) {
	n, err := strconv.Atoi(h.VID)
	if err != nil || n < 1 {
		return 0, false
	}

	return n, true
}

// BridgeHosts returns the bridge's learned hardware addresses.
func (r *RouterOS) BridgeHosts(ctx context.Context) ([]BridgeHost, error) {
	return list[BridgeHost](ctx, r, bridgeHostAPI)
}
