package openwrt

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
)

// IfaceAddr is one address an interface holds, with its prefix length.
type IfaceAddr struct {
	Address string `json:"address"`
	Mask    int    `json:"mask"`
}

// PrefixAssignment is an IPv6 prefix netifd assigned to an interface from a
// delegated one, which makes the interface a segment the router serves.
type PrefixAssignment struct {
	Address string `json:"address"`
	Mask    int    `json:"mask"`
}

// Interface is one logical interface from netifd, such as "lan" or "guest":
// the name an operator gave a segment, the device under it and the addresses
// the router holds on it.
type Interface struct {
	// Name is the interface's name in /etc/config/network.
	Name string `json:"interface"`

	Up bool `json:"up"`

	// Proto is how the interface gets its addresses: "static" for one the
	// router serves, "dhcp" or "pppoe" for an uplink.
	Proto string `json:"proto"`

	// Device is the device the interface is configured on, and L3Device the
	// one its addresses sit on. They differ when a protocol such as pppoe
	// creates a device of its own.
	Device   string `json:"device"`
	L3Device string `json:"l3_device"`

	IPv4 []IfaceAddr `json:"ipv4-address"`
	IPv6 []IfaceAddr `json:"ipv6-address"`

	// Assigned are the IPv6 prefixes assigned to the interface.
	Assigned []PrefixAssignment `json:"ipv6-prefix-assignment"`
}

// ProtoStatic is the protocol of an interface with addresses an operator
// configured, which is how a segment the router serves is set up.
const ProtoStatic = "static"

// Interfaces returns every logical interface netifd knows, up or not.
func (o *OpenWrt) Interfaces(ctx context.Context) ([]Interface, error) {
	res, err := call[struct {
		Interfaces []Interface `json:"interface"`
	}](ctx, o, "network.interface", "dump", nil)
	if err != nil {
		return nil, err
	}

	return res.Interfaces, nil
}

// VLANs returns the VLAN tag of each VLAN device in /etc/config/network, keyed
// by device name. Nothing netifd reports at run time carries the tag, so the
// configuration is the only place it can come from.
//
// Two kinds of section define one. A device section of type 8021q or 8021ad
// tags a device such as eth0, and names the result eth0.20 unless it gives a
// name. A bridge-vlan section tags a VLAN-aware bridge, and the result is
// always named after the bridge and the tag, such as br-lan.20.
func (o *OpenWrt) VLANs(ctx context.Context) (map[string]int, error) {
	out := make(map[string]int)

	var errs []error

	devices, err := uciSections(ctx, o, "network", "device")
	if err != nil {
		errs = append(errs, fmt.Errorf("vlan devices: %w", err))
	}

	for _, s := range devices {
		switch first(uciValue(s["type"])) {
		case "8021q", "8021ad":
		default:
			continue
		}

		vid, ok := tag(first(uciValue(s["vid"])))
		if !ok {
			continue
		}

		name := first(uciValue(s["name"]))
		if name == "" {
			name = first(uciValue(s["ifname"])) + "." + strconv.Itoa(vid)
		}

		out[name] = vid
	}

	bridged, err := uciSections(ctx, o, "network", "bridge-vlan")
	if err != nil {
		errs = append(errs, fmt.Errorf("bridge vlans: %w", err))
	}

	for _, s := range bridged {
		vid, ok := tag(first(uciValue(s["vlan"])))
		if !ok {
			continue
		}

		if bridge := first(uciValue(s["device"])); bridge != "" {
			out[bridge+"."+strconv.Itoa(vid)] = vid
		}
	}

	return out, errors.Join(errs...)
}

// tag reads a VLAN ID, which 802.1Q bounds to 1-4094.
func tag(s string) (int, bool) {
	vid, err := strconv.Atoi(s)
	if err != nil || vid < 1 || vid > 4094 {
		return 0, false
	}

	return vid, true
}

// section is one uci section, as option name to value.
type section map[string]jsontext.Value

// uciSections returns the sections of one type in one config file, in the
// order the file lists them. Section names do not give that order, since an
// anonymous section is named cfg followed by a hash.
func uciSections(ctx context.Context, o *OpenWrt, config, typ string) ([]section, error) {
	res, err := call[struct {
		Values map[string]section `json:"values"`
	}](ctx, o, "uci", "get", map[string]string{"config": config, "type": typ})
	if err != nil {
		return nil, err
	}

	out := slices.Collect(maps.Values(res.Values))

	slices.SortFunc(out, func(a, b section) int { return cmp.Compare(a.index(), b.index()) })

	return out, nil
}

// index is the section's position in its file, from uci's .index.
func (s section) index() int {
	var i int

	_ = json.Unmarshal(s[".index"], &i)

	return i
}
