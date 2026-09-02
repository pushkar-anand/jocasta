package routeros

import "context"

// ARPEntry is one row of /ip/arp: an address the router has resolved to a
// hardware address.
//
// The router is the gateway for every VLAN it serves, so its table covers
// segments a sweep run from one host cannot see. That is the reason this
// client exists.
type ARPEntry struct {
	// ID is RouterOS's internal handle, such as "*1A". It is stable only
	// within a boot, so it identifies a row and not a device.
	ID string `json:".id"`

	// Address and MACAddress are left as the router rendered them. An
	// incomplete entry carries the all-zero hardware address, which parses
	// perfectly well and means nothing.
	Address    string `json:"address"`
	MACAddress string `json:"mac-address"`

	// Interface is the bridge or VLAN interface the entry was learned on,
	// which is the closest thing here to where the device physically is.
	Interface string `json:"interface"`

	// Complete says the resolution finished. An incomplete entry is an address
	// the router asked about and got no answer for, so it names no device.
	Complete Bool `json:"complete"`

	// Dynamic separates what the router learned from what an operator typed.
	// A static entry is configuration and may name a device that has not been
	// on the network in months.
	Dynamic Bool `json:"dynamic"`

	// Invalid marks an entry the router itself no longer believes, and
	// Disabled one an operator turned off. Neither is evidence of anything.
	Invalid   Bool `json:"invalid"`
	Disabled  Bool `json:"disabled"`
	Published Bool `json:"published"`

	// Status is the neighbour state on RouterOS 7 -- "reachable", "stale",
	// "delay", "failed". Older tables leave it empty, so Complete rather than
	// this is what a caller should test.
	Status string `json:"status"`

	Comment string `json:"comment"`
}

// Usable reports whether the entry is evidence that a device holds this
// address now: resolved, believed, and switched on.
func (e ARPEntry) Usable() bool {
	return bool(e.Complete) && !bool(e.Invalid) && !bool(e.Disabled)
}

// ARP returns the router's ARP table, every VLAN of it.
func (r *RouterOS) ARP(ctx context.Context) ([]ARPEntry, error) {
	return list[ARPEntry](ctx, r, arpAPI)
}
