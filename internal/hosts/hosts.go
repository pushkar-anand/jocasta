// Package hosts turns the raw attributes that a source reports about a device
// into an enriched host. The attributes are an address, a hardware address,
// and sometimes a name. The package parses the address and finds the vendor
// from the OUI. If the caller can claim the result, it also does a reverse
// lookup.
//
// A sweep and a router plugin both build through [BuildHost]. So the
// description of a device is the same, for either source.
package hosts

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pushkar-anand/jocasta/pkg/oui"
)

// zeroMAC parses, but it identifies nothing. An unresolved neighbour entry has
// it. So it must not become an identity that two devices share.
const zeroMAC = "00:00:00:00:00:00"

// maxNameLength is the longest name in DNS, without the root dot.
const maxNameLength = 253

// CleanName returns name without whitespace around it. It reports whether the
// result is usable as a hostname. A usable name is valid UTF-8 of 1 to 253
// bytes, with no control or format character. Format characters include
// U+202E, which reverses the display of the rest of the name. When ok is
// false, the name is "".
//
// A router or DHCP server passes on any name that a client asked for. So a
// name from any source goes through this check before Jocasta keeps it.
func CleanName(name string) (cleaned string, ok bool) {
	name = strings.TrimSpace(name)

	if name == "" || len(name) > maxNameLength || !utf8.ValidString(name) {
		return "", false
	}

	for _, r := range name {
		if unicode.In(r, unicode.Cc, unicode.Cf) {
			return "", false
		}
	}

	return name, true
}

// CanonicalMAC renders a hardware address in the lowercase colon-separated
// form. So addresses from different sources compare equal, in any input form.
// An empty or all-zero address names no hardware. It returns "" with ok true.
// ok is false only when s does not parse.
func CanonicalMAC(s string) (mac string, ok bool) {
	if s == "" {
		return "", true
	}

	hw, err := net.ParseMAC(s)
	if err != nil {
		return "", false
	}

	if mac = hw.String(); mac == zeroMAC {
		return "", true
	}

	return mac, true
}

// Host is a device as one source sees it. It keeps the strings of the source
// exactly. It also has the typed values and vendor metadata that [BuildHost]
// got from them.
type Host struct {
	IP  string
	MAC string

	// Interface is the network interface on which the source saw the device,
	// if the source reports one.
	Interface string

	// VLAN is the 802.1Q tag of the device. It is zero when no tag is known.
	VLAN int

	addr     netip.Addr
	mac      net.HardwareAddr
	hostname string

	// reverseNames is every name from the reverse lookup, sorted. hostname is
	// the first. It is empty when no lookup ran, or it gave no usable name.
	reverseNames []string

	// vendor and shortName come from the OUI. They are the full registered
	// name and a shorter display form. A locally administered address can
	// also have them, because some vendors use a prefix that they never
	// registered.
	vendor    string
	shortName string

	// randomised marks an address with its locally administered bit set.
	// Privacy addresses set it. Virtual and container interfaces also set it.
	// So a device with a vendor match can still be randomised here.
	randomised bool
}

// HostInput holds the raw attributes that a source reports for one host. It is
// the whole input to [BuildHost]. So a caller names each field at the call
// site.
type HostInput struct {
	IP  string
	MAC string

	// Hostname, when set, is the name that the source already has for the
	// host, such as a DHCP lease name. If [CleanName] rejects it, there is no
	// name.
	Hostname string

	// ResolveName asks for a reverse DNS lookup when Hostname is empty or
	// rejected.
	//
	// It is off by default, because a resolved name belongs to the source
	// that resolved it. A sweep that asks the local resolver about an address
	// that it just probed reports its own finding, so it sets this. The ARP
	// table of a router is not such a finding. If this host's resolver filled
	// its rows that have no name, one source's name would go under the claim
	// of a different source.
	ResolveName bool

	Interface string
	VLAN      int
}

// BuildHost parses and enriches one host from what a source reported about it.
//
// A malformed address or hardware address is an error. All other steps are
// best-effort. So a host with no OUI match and no reverse name still builds.
func BuildHost(ctx context.Context, in HostInput) (*Host, error) {
	h := &Host{
		IP:        in.IP,
		MAC:       in.MAC,
		Interface: in.Interface,
		VLAN:      in.VLAN,
	}

	h.hostname, _ = CleanName(in.Hostname)

	if in.IP != "" {
		ipAddr, err := netip.ParseAddr(in.IP)
		if err != nil {
			return nil, fmt.Errorf("parse address %q: %w", in.IP, err)
		}

		h.addr = ipAddr
	}

	// This needs ResolveName and a missing name, because a resolved name
	// belongs to the source that resolved it. See [HostInput.ResolveName].
	if in.ResolveName && h.hostname == "" && h.addr.IsValid() {
		for _, n := range resolveNames(ctx, h.addr) {
			if name, ok := CleanName(n); ok {
				h.reverseNames = append(h.reverseNames, name)
			}
		}

		if len(h.reverseNames) > 0 {
			h.hostname = h.reverseNames[0]
		}
	}

	if in.MAC != "" {
		hw, err := net.ParseMAC(in.MAC)
		if err != nil {
			return nil, fmt.Errorf("parse MAC %q: %w", in.MAC, err)
		}

		h.mac = hw

		// Read the vendor match and the locally administered bit separately.
		// They answer different questions, and an address can have both. The
		// manuf file of Wireshark has a few hundred prefixes that a vendor
		// chose without a registration. A device with such a prefix is
		// hardware of that vendor, for any value of the address bit. For a
		// true privacy address, a caller needs [Host.Randomised] and an empty
		// [Host.Vendor] together.
		if info, found := oui.Lookup(hw); found {
			h.shortName = info.Short
			h.vendor = info.Name
		}

		if oui.IsLocallyAdministered(hw) {
			h.randomised = true
		}
	}

	return h, nil
}

// Address is the parsed IP address. It is invalid when the source reported
// none.
func (h Host) Address() netip.Addr {
	return h.addr
}

// Vendor is the full organisation name from the OUI. It is empty when the
// address matched no registered prefix.
func (h Host) Vendor() string {
	return h.vendor
}

// Hostname is the name from the source or from the reverse lookup. It is
// empty when neither gave one.
func (h Host) Hostname() string {
	return h.hostname
}

// ReverseNames returns every name from the reverse lookup, sorted. If the name
// came from that lookup, Hostname is the first. An address can have several
// names, such as the name of a server and the name of a role that it serves.
func (h Host) ReverseNames() []string {
	return h.reverseNames
}

// Named returns a copy of h with name. Use it for a source that learns the
// name after the host is built. If [CleanName] rejects the name, the copy has
// no name.
func (h Host) Named(name string) *Host {
	h.hostname, _ = CleanName(name)

	return &h
}

// ShortName is the vendor in display form. If there is none, it is the full
// name, or empty.
func (h Host) ShortName() string {
	return h.shortName
}

// HardwareAddress is the parsed hardware address. It is nil when the source
// reported none.
func (h Host) HardwareAddress() net.HardwareAddr {
	return h.mac
}

// Randomised reports whether the address has its locally administered bit set.
// Privacy addresses set the bit. Virtual and container interfaces also set
// it. So a randomised host can still have a vendor.
func (h Host) Randomised() bool {
	return h.randomised
}

// MarshalJSON writes the enriched values of a Host. The name, vendor and
// randomised flag are in unexported fields. So the default encoding writes a
// Host without its useful half.
func (h Host) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		IP         string `json:"ip"`
		MAC        string `json:"mac,omitempty"`
		Hostname   string `json:"hostname,omitempty"`
		Vendor     string `json:"vendor,omitempty"`
		ShortName  string `json:"vendor_short,omitempty"`
		Randomised bool   `json:"randomised,omitempty"`
		Interface  string `json:"interface,omitempty"`
		VLAN       int    `json:"vlan,omitempty"`
	}{
		IP:         h.IP,
		MAC:        h.MAC,
		Hostname:   h.hostname,
		Vendor:     h.vendor,
		ShortName:  h.shortName,
		Randomised: h.randomised,
		Interface:  h.Interface,
		VLAN:       h.VLAN,
	})
}
