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

// NetDevice is one network device, as the rpcd module of LuCI describes it: a
// port, a bridge, the interface of a radio, or a VLAN device.
type NetDevice struct {
	Name string `json:"name"`

	// DevType is the device type of the kernel, such as "ethernet", "dsa",
	// "bridge", "vlan" or "wlan". The user port of a DSA switch, such as
	// lan1, reports "dsa".
	DevType string `json:"devtype"`

	// Type is the hardware type from if_arp.h: 1 for Ethernet, 772 for
	// loopback.
	Type int `json:"type"`

	Up       bool `json:"up"`
	Bridge   bool `json:"bridge"`
	Wireless bool `json:"wireless"`

	// Ports are the member devices of a bridge.
	Ports []string `json:"ports"`

	MAC string `json:"mac"`

	Link struct {
		// Speed is the rate at which the link came up, in Mbit/s. It is
		// absent when the driver does not report a rate.
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

// IsWireless reports whether the device is the interface of a radio.
func (d NetDevice) IsWireless() bool { return d.Wireless || d.DevType == "wlan" }

// IsPort reports whether the device is a port that a device can connect to:
// an Ethernet port, the user port of a DSA switch, or the interface of a
// radio. On a bridge, a VLAN device, a tunnel or loopback, the router can only
// learn an address.
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

// Radio is one Wi-Fi radio, with the networks that it serves.
type Radio struct {
	Name string
	Up   bool

	// Band is the band of the radio, such as "2g", "5g" or "6g". It is empty
	// on a release before 21.02, which did not configure a band.
	Band string

	Interfaces []WiFiIface
}

// WiFiIface is one network that a radio serves, such as an access point for
// one SSID.
type WiFiIface struct {
	// Ifname is the device on which the network runs, such as "phy0-ap0". It
	// is empty while the network is down.
	Ifname string

	SSID string

	// Mode is "ap" for an access point, or "sta", "mesh" or "monitor".
	Mode string
}

// rawRadio is a radio as network.wireless status renders it. It includes the
// iwinfo data that LuCI adds to each interface.
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

// WirelessDevices returns the radios of the router, sorted by name. A router
// with no radio returns none.
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

// Station is one client on the network of a radio.
type Station struct {
	MAC string

	// Signal is the signal of the client at the radio, in dBm. It is zero
	// when the driver does not report it.
	Signal int

	// TxRate is the rate at which the radio sends to the client. RxRate is the
	// rate at which it receives from the client. Both are in bits per second.
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

// Stations returns the clients on the network of device, such as
// "phy0-ap0".
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

// FDBEntry is one hardware address that a bridge learned on one of its ports.
type FDBEntry struct {
	MAC string

	// Port is the member device on which the bridge learned the address, such
	// as "lan2". It is empty when the port number matches no member.
	Port string

	// Local marks an own address of the bridge.
	Local bool
}

// fdbEntrySize is the size of one struct __fdb_entry in brforward, from
// linux/if_bridge.h. The struct has the address, the low byte of the port
// number, the local flag, the ageing timer, the high byte of the port number,
// and padding.
const fdbEntrySize = 16

// fileReadCap is the maximum that the file read of rpcd returns from a file
// when the kernel does not report its size. brforward is such a file. The
// maximum is one read of 4096 bytes, which is 256 bridge entries.
const fileReadCap = 4096

var (
	// ErrBadFDB is a bridge table that is not a whole number of entries.
	ErrBadFDB = errors.New("openwrt: bridge table is not a whole number of entries")

	// ErrFDBCapped is a bridge table that filled the maximum of the file read
	// of rpcd. So entries after the first 256 can be missing.
	ErrFDBCapped = errors.New("openwrt: bridge table filled rpcd's 4096-byte read, so entries past 256 may be missing")
)

// BridgeFDB returns the hardware addresses that bridge learned, each on the
// member port on which it learned it. members are the ports of the bridge.
// Their port numbers tell which entry is on which port.
//
// The table comes from /sys/class/net/<bridge>/brforward of the kernel,
// through the file read of rpcd. It needs no package. The number of each
// member comes from its brport/port_no. If BridgeFDB cannot read the number of
// a member, the entries of that member have no port. BridgeFDB then also
// returns the error.
//
// rpcd reads at most 4096 bytes of the table, which is 256 entries. It does
// not tell when there are more. If a table fills the read, BridgeFDB returns
// [ErrFDBCapped]. A command can read the whole table only with an rpcd ACL
// that lets the login read any file. So the limit stays.
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

// portNumber reads the port number that a bridge gives its member device.
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

// RawFDBEntry is one entry of a brforward table, before BridgeFDB matches its
// port number to a member.
type RawFDBEntry struct {
	MAC    string
	PortNo int
	Local  bool
}

// ParseFDB reads a brforward table. If the table ends with a part of an
// entry, ParseFDB reports it, with the whole entries before it.
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

// BridgeVLANPort is one port that a bridge VLAN carries.
type BridgeVLANPort struct {
	Name string

	// Tagged marks a port that carries the VLAN tagged. PVID marks a port
	// whose untagged traffic belongs to the VLAN.
	Tagged bool
	PVID   bool
}

// BridgeVLAN is one VLAN on a VLAN-aware bridge, with the ports that carry it.
type BridgeVLAN struct {
	Bridge string
	VLAN   int
	Ports  []BridgeVLANPort
}

// BridgeVLANs returns the bridge-vlan sections of /etc/config/network, in the
// order of the file.
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

// ParseBridgeVLANPort reads one entry of the ports list of a bridge-vlan
// section, such as "lan1", "lan2:t" or "lan3:u*". A port is untagged, unless
// it has ":t". "*" makes the VLAN its PVID.
func ParseBridgeVLANPort(s string) BridgeVLANPort {
	name, flags, _ := strings.Cut(s, ":")

	return BridgeVLANPort{
		Name:   name,
		Tagged: strings.Contains(flags, "t"),
		PVID:   strings.Contains(flags, "*"),
	}
}
