package routeros

import (
	"context"
	"strconv"
)

// VLAN is one row of /interface/vlan: an 802.1Q tag, with the name of the
// interface that carries it.
//
// An address knows only its interface. So this table changes "the segment on
// vlan20" into "VLAN 20".
type VLAN struct {
	ID string `json:".id"`

	// Name is the interface name. An address refers to this VLAN by this
	// name.
	Name string `json:"name"`

	// VLANID is the 802.1Q tag, as a string. The REST service returns every
	// number as a string. See [VLAN.Tag].
	VLANID string `json:"vlan-id"`

	// Interface is the parent of the tagged traffic, usually a bridge.
	Interface string `json:"interface"`

	Disabled Bool `json:"disabled"`
	Running  Bool `json:"running"`

	// Comment is the note of the operator. On a VLAN, it is usually the name
	// of the segment: "IoT", "Guest".
	Comment string `json:"comment"`
}

// Tag returns the 802.1Q tag as a number. It returns false when the router
// rendered something that is not a number. Outside this router, only the tag
// identifies the segment. So a row without a tag names nothing.
func (v VLAN) Tag() (tag int, ok bool) {
	n, err := strconv.Atoi(v.VLANID)
	if err != nil {
		return 0, false
	}

	return n, true
}

// VLANs returns the tagged interfaces that the router defines.
func (r *RouterOS) VLANs(ctx context.Context) ([]VLAN, error) {
	return list[VLAN](ctx, r, vlanAPI)
}
