package routeros

import (
	"context"
	"strconv"
)

// VLAN is one row of /interface/vlan: an 802.1Q tag and the interface name
// that carries it.
//
// An address knows its interface and nothing else, so this table is what turns
// "the segment on vlan20" into "VLAN 20".
type VLAN struct {
	ID string `json:".id"`

	// Name is the interface name, which is how an address refers to this VLAN.
	Name string `json:"name"`

	// VLANID is the 802.1Q tag, rendered as a string like every other number
	// the REST service returns. See [VLAN.Tag].
	VLANID string `json:"vlan-id"`

	// Interface is the parent the tagged traffic runs over, typically a bridge.
	Interface string `json:"interface"`

	Disabled Bool `json:"disabled"`
	Running  Bool `json:"running"`

	// Comment is the operator's note, which on a VLAN is usually what they call
	// the segment: "IoT", "Guest".
	Comment string `json:"comment"`
}

// Tag returns the 802.1Q tag as a number, and false when the router rendered
// something that is not one. A tag is what identifies the segment to anything
// outside this router, so a row without one names nothing.
func (v VLAN) Tag() (tag int, ok bool) {
	n, err := strconv.Atoi(v.VLANID)
	if err != nil {
		return 0, false
	}

	return n, true
}

// VLANs returns the tagged interfaces the router defines.
func (r *RouterOS) VLANs(ctx context.Context) ([]VLAN, error) {
	return list[VLAN](ctx, r, vlanAPI)
}
