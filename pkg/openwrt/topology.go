package openwrt

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
)

// NetDevice is one network device as LuCI's rpcd module describes it: a port,
// a bridge, a radio's interface or a VLAN device.
type NetDevice struct {
	Name string `json:"name"`

	// DevType is the kernel's device type, such as "ethernet", "dsa",
	// "bridge", "vlan" or "wlan". A DSA switch's user port, such as lan1,
	// reports "dsa".
	DevType string `json:"devtype"`

	// Type is the hardware type from if_arp.h: 1 for Ethernet, 772 for
	// loopback.
	Type int `json:"type"`

	Up       bool `json:"up"`
	Bridge   bool `json:"bridge"`
	Wireless bool `json:"wireless"`

	// Ports are a bridge's member devices.
	Ports []string `json:"ports"`

	MAC string `json:"mac"`

	Link struct {
		// Speed is the rate the link came up at in Mbit/s, and absent when
		// the driver does not report one.
		Speed int `json:"speed"`

		// Duplex is "full", "half" or "unknown".
		Duplex string `json:"duplex"`

		// Carrier reports whether the link is up at the physical layer.
		Carrier bool `json:"carrier"`
	} `json:"link"`
}

// arpHrdLoopback is the hardware type of a loopback device, from if_arp.h.
const arpHrdLoopback = 772

// IsLoopback reports whether the device is the loopback device.
func (d NetDevice) IsLoopback() bool { return d.Type == arpHrdLoopback }

// IsWireless reports whether the device is a radio's interface.
func (d NetDevice) IsWireless() bool { return d.Wireless || d.DevType == "wlan" }

// IsPort reports whether the device is a port something can be plugged into:
// an Ethernet port, a DSA switch's user port or a radio's interface. A bridge,
// a VLAN device, a tunnel and loopback are only places an address can be
// learned.
func (d NetDevice) IsPort() bool {
	if d.Bridge || d.IsLoopback() {
		return false
	}

	return d.IsWireless() || d.DevType == "ethernet" || d.DevType == "dsa"
}

// NetworkDevices returns every network device on the router, keyed by name.
func (o *OpenWrt) NetworkDevices(ctx context.Context) (map[string]NetDevice, error) {
	res, err := call[map[string]NetDevice](ctx, o, "luci-rpc", "getNetworkDevices", nil)
	if err != nil {
		return nil, err
	}

	return *res, nil
}

// Radio is one Wi-Fi radio and the networks it serves.
type Radio struct {
	Name string
	Up   bool

	// Band is the radio's band, such as "2g", "5g" or "6g", and is empty on a
	// release older than 21.02, which did not configure one.
	Band string

	Interfaces []WiFiIface
}

// WiFiIface is one network a radio serves, such as an access point for one
// SSID.
type WiFiIface struct {
	// Ifname is the device the network runs on, such as "phy0-ap0", and is
	// empty while the network is not up.
	Ifname string

	SSID string

	// Mode is "ap" for an access point, or "sta", "mesh" or "monitor".
	Mode string
}

// rawRadio is a radio as network.wireless status renders it, with the iwinfo
// LuCI adds to each interface.
type rawRadio struct {
	Up     bool `json:"up"`
	Config struct {
		Band string `json:"band"`
	} `json:"config"`
	Interfaces []struct {
		Ifname string `json:"ifname"`
		Config struct {
			SSID string `json:"ssid"`
			Mode string `json:"mode"`
		} `json:"config"`
		IWInfo struct {
			SSID string `json:"ssid"`
		} `json:"iwinfo"`
	} `json:"interfaces"`
}

// WirelessDevices returns the router's radios, sorted by name. A router with
// no radio returns none.
func (o *OpenWrt) WirelessDevices(ctx context.Context) ([]Radio, error) {
	res, err := call[map[string]rawRadio](ctx, o, "luci-rpc", "getWirelessDevices", nil)
	if err != nil {
		return nil, err
	}

	out := make([]Radio, 0, len(*res))

	for name, r := range *res {
		radio := Radio{Name: name, Up: r.Up, Band: r.Config.Band}

		for _, i := range r.Interfaces {
			radio.Interfaces = append(radio.Interfaces, WiFiIface{
				Ifname: i.Ifname,
				SSID:   firstNonEmpty(i.IWInfo.SSID, i.Config.SSID),
				Mode:   i.Config.Mode,
			})
		}

		out = append(out, radio)
	}

	slices.SortFunc(out, func(a, b Radio) int { return strings.Compare(a.Name, b.Name) })

	return out, nil
}

// firstNonEmpty returns the first of values that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

// Station is one client associated with a radio's network.
type Station struct {
	MAC string

	// Signal is the client's signal at the radio in dBm, and zero when the
	// driver does not report it.
	Signal int

	// TxRate is the rate the radio sends to the client at and RxRate the rate
	// it receives from it at, in bits per second.
	TxRate int64
	RxRate int64
}

// rawStation is a station as iwinfo renders it. Rates are in kbit/s.
type rawStation struct {
	MAC    string `json:"mac"`
	Signal int    `json:"signal"`
	RX     struct {
		Rate int64 `json:"rate"`
	} `json:"rx"`
	TX struct {
		Rate int64 `json:"rate"`
	} `json:"tx"`
}

// Stations returns the clients associated with the network on device, such
// as "phy0-ap0".
func (o *OpenWrt) Stations(ctx context.Context, device string) ([]Station, error) {
	res, err := call[struct {
		Results []rawStation `json:"results"`
	}](ctx, o, "iwinfo", "assoclist", map[string]string{"device": device})
	if err != nil {
		return nil, err
	}

	out := make([]Station, 0, len(res.Results))

	for _, s := range res.Results {
		out = append(out, Station{
			MAC:    s.MAC,
			Signal: s.Signal,
			TxRate: s.TX.Rate * 1000,
			RxRate: s.RX.Rate * 1000,
		})
	}

	return out, nil
}

// FDBEntry is one hardware address a bridge learned on one of its ports.
type FDBEntry struct {
	MAC string

	// Port is the member device the address was learned on, such as "lan2",
	// and is empty when the port number matches no member.
	Port string

	// Local marks one of the bridge's own addresses.
	Local bool
}

// fdbEntrySize is the size of one struct __fdb_entry in brforward, from
// linux/if_bridge.h: the address, the port number's low byte, the local flag,
// the ageing timer, the port number's high byte and padding.
const fdbEntrySize = 16

// fileReadCap is the most rpcd's file read returns from a file whose size the
// kernel does not report, as brforward's is not: one read of 4096 bytes. That
// is 256 bridge entries.
const fileReadCap = 4096

var (
	// ErrBadFDB is a bridge table that is not a whole number of entries.
	ErrBadFDB = errors.New("openwrt: bridge table is not a whole number of entries")

	// ErrFDBCapped is a bridge table that filled the most rpcd's file read
	// returns, so entries past the first 256 may be missing.
	ErrFDBCapped = errors.New("openwrt: bridge table filled rpcd's 4096-byte read, so entries past 256 may be missing")
)

// BridgeFDB returns the hardware addresses bridge has learned, each on the
// member port it was learned on. members are the bridge's ports, whose port
// numbers say which entry is on which.
//
// The table comes from the kernel's /sys/class/net/<bridge>/brforward, which
// needs no package, through rpcd's file read. Each member's number comes from
// its brport/port_no. A member whose number cannot be read leaves its entries
// with no port, alongside the error.
//
// rpcd reads at most 4096 bytes of the table, which is 256 entries, and says
// nothing when there are more. A table that fills the read comes back with
// [ErrFDBCapped]. No command reads it whole without an rpcd ACL that would let
// the login read any file, so the cap stands.
func (o *OpenWrt) BridgeFDB(ctx context.Context, bridge string, members []string) ([]FDBEntry, error) {
	res, err := call[struct {
		Data string `json:"data"`
	}](ctx, o, "file", "read", map[string]any{
		"path":   "/sys/class/net/" + bridge + "/brforward",
		"base64": true,
	})
	if err != nil {
		return nil, fmt.Errorf("bridge table of %s: %w", bridge, err)
	}

	raw, err := base64.StdEncoding.DecodeString(res.Data)
	if err != nil {
		return nil, fmt.Errorf("bridge table of %s: %w", bridge, err)
	}

	var errs []error

	numbers := make(map[int]string, len(members))

	for _, m := range members {
		n, err := o.portNumber(ctx, m)
		if err != nil {
			errs = append(errs, fmt.Errorf("port number of %s: %w", m, err))

			continue
		}

		numbers[n] = m
	}

	entries, err := ParseFDB(raw)
	if err != nil {
		errs = append(errs, fmt.Errorf("bridge table of %s: %w", bridge, err))
	}

	if len(raw) >= fileReadCap {
		errs = append(errs, fmt.Errorf("bridge table of %s: %w", bridge, ErrFDBCapped))
	}

	out := make([]FDBEntry, 0, len(entries))

	for _, e := range entries {
		out = append(out, FDBEntry{MAC: e.MAC, Port: numbers[e.PortNo], Local: e.Local})
	}

	return out, errors.Join(errs...)
}

// portNumber reads the port number a bridge gives its member device.
func (o *OpenWrt) portNumber(ctx context.Context, device string) (int, error) {
	res, err := call[struct {
		Data string `json:"data"`
	}](ctx, o, "file", "read", map[string]any{"path": "/sys/class/net/" + device + "/brport/port_no"})
	if err != nil {
		return 0, err
	}

	// The kernel prints it in hex, as "0x1".
	n, err := strconv.ParseInt(strings.TrimSpace(res.Data), 0, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: port number %q", ErrUnexpected, res.Data)
	}

	return int(n), nil
}

// RawFDBEntry is one entry of a brforward table, before its port number is
// matched to a member.
type RawFDBEntry struct {
	MAC    string
	PortNo int
	Local  bool
}

// ParseFDB reads a brforward table. A trailing part entry is reported, with
// the whole entries before it.
func ParseFDB(b []byte) ([]RawFDBEntry, error) {
	out := make([]RawFDBEntry, 0, len(b)/fdbEntrySize)

	for len(b) >= fdbEntrySize {
		e := b[:fdbEntrySize]
		b = b[fdbEntrySize:]

		out = append(out, RawFDBEntry{
			MAC:    net.HardwareAddr(e[:6]).String(),
			PortNo: int(e[12])<<8 | int(e[6]),
			Local:  e[7] != 0,
		})
	}

	if len(b) != 0 {
		return out, ErrBadFDB
	}

	return out, nil
}

// BridgeVLANPort is one port a bridge VLAN carries.
type BridgeVLANPort struct {
	Name string

	// Tagged marks a port that carries the VLAN tagged, and PVID one whose
	// untagged traffic belongs to it.
	Tagged bool
	PVID   bool
}

// BridgeVLAN is one VLAN on a VLAN-aware bridge and the ports that carry it.
type BridgeVLAN struct {
	Bridge string
	VLAN   int
	Ports  []BridgeVLANPort
}

// BridgeVLANs returns the bridge-vlan sections of /etc/config/network, in the
// order the file lists them.
func (o *OpenWrt) BridgeVLANs(ctx context.Context) ([]BridgeVLAN, error) {
	sections, err := uciSections(ctx, o, "network", "bridge-vlan")
	if err != nil {
		return nil, err
	}

	out := make([]BridgeVLAN, 0, len(sections))

	for _, s := range sections {
		vid, ok := tag(first(uciValue(s["vlan"])))
		if !ok {
			continue
		}

		v := BridgeVLAN{Bridge: first(uciValue(s["device"])), VLAN: vid}

		for _, p := range uciValue(s["ports"]) {
			v.Ports = append(v.Ports, ParseBridgeVLANPort(p))
		}

		out = append(out, v)
	}

	return out, nil
}

// ParseBridgeVLANPort reads one entry of a bridge-vlan section's ports list,
// such as "lan1", "lan2:t" or "lan3:u*". A port is untagged unless it says
// ":t", and "*" makes the VLAN its PVID.
func ParseBridgeVLANPort(s string) BridgeVLANPort {
	name, flags, _ := strings.Cut(s, ":")

	return BridgeVLANPort{
		Name:   name,
		Tagged: strings.Contains(flags, "t"),
		PVID:   strings.Contains(flags, "*"),
	}
}
