package routeros

import "context"

// ARPEntry is one row of /ip/arp: an address that the router resolved to a
// hardware address.
//
// The router is the gateway for every VLAN that it serves. So its table covers
// segments that a sweep from one host cannot see. This client exists for that
// reason.
type ARPEntry struct {
	// ID is the internal handle of RouterOS, such as "*1A". It is stable only
	// during one boot, so it identifies only a row.
	ID string `json:".id"`

	// Address and MACAddress stay as the router rendered them. An incomplete
	// entry has the all-zero hardware address. This address parses, but it
	// means nothing.
	Address    string `json:"address"`
	MACAddress string `json:"mac-address"`

	// Interface is the bridge or VLAN interface on which the router learned
	// the entry. It is the nearest clue here to where the device is.
	Interface string `json:"interface"`

	// Complete tells that the resolution finished. For an incomplete entry,
	// the router asked about the address and got no answer. So the entry names
	// no device.
	Complete Bool `json:"complete"`

	// Dynamic separates what the router learned from what an operator typed.
	// A static entry is configuration. It can name a device that was not on
	// the network for months.
	Dynamic Bool `json:"dynamic"`

	// Invalid marks an entry that the router no longer trusts. Disabled marks
	// an entry that an operator turned off. Neither one proves anything.
	Invalid   Bool `json:"invalid"`
	Disabled  Bool `json:"disabled"`
	Published Bool `json:"published"`

	// Status is the neighbour state on RouterOS 7: "reachable", "stale",
	// "delay", "probe" or "failed". Older tables leave it empty. There, only
	// Complete tells whether the entry names a device.
	Status string `json:"status"`

	Comment string `json:"comment"`
}

// arpReachable is the RouterOS 7 neighbour state. It means that the router
// confirmed that the address answers within its reachable-time.
const arpReachable = "reachable"

// Usable reports whether the entry names a device that the router trusts. The
// entry must be resolved to a hardware address, valid, and enabled.
// [ARPEntry.Reachable] tells whether that device answers now.
func (e ARPEntry) Usable() bool {
	return bool(e.Complete) && !bool(e.Invalid) && !bool(e.Disabled)
}

// Reachable reports whether the router heard from this address recently. On
// RouterOS 7, this is the "reachable" neighbour state. The states "stale",
// "delay", "probe" and "failed" all mean that it did not. This is true even
// if the router still has a hardware address. RouterOS 6 has no neighbour
// state. There, a usable entry is the best signal, so Reachable uses it.
//
// This is the presence test. Through Usable, a stale entry keeps a device in
// the inventory. But it must not make the device show as online.
func (e ARPEntry) Reachable() bool {
	if e.Status == "" {
		return e.Usable()
	}

	return e.Status == arpReachable
}

// ARP returns the ARP table of the router, for every VLAN.
func (r *RouterOS) ARP(ctx context.Context) ([]ARPEntry, error) {
	return list[ARPEntry](ctx, r, arpAPI)
}
