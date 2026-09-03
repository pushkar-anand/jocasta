// Package hosts turns the raw attributes a source reports about a device --
// an address, a hardware address, sometimes a name -- into an enriched host,
// with the address parsed, the vendor resolved from the OUI, and a reverse
// lookup done when the caller is entitled to claim the result.
//
// A sweep and a router plugin both build through [BuildHost], so one device is
// described the same way whichever of them found it.
package hosts

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"

	"github.com/pushkar-anand/jocasta/pkg/oui"
)

// Host is a device as one source sees it: the strings the source reported,
// kept verbatim, alongside the typed values and vendor metadata [BuildHost]
// worked out from them.
type Host struct {
	IP  string
	MAC string

	// Interface is the network interface the device was seen on, where the
	// source reports one.
	Interface string

	// VLAN is the 802.1Q tag the device sits behind, zero when none is known.
	VLAN int

	addr     netip.Addr
	mac      net.HardwareAddr
	hostname string

	// vendor and shortName come from the OUI: the full registered name and a
	// shorter display form. A locally administered address can carry them too,
	// since some vendors use a prefix they never registered.
	vendor    string
	shortName string

	// randomised marks an address with its locally administered bit set. Privacy
	// addresses set it, but so do virtual and container interfaces, so a device
	// with a vendor match can still read as randomised here.
	randomised bool
}

// HostInput carries the raw attributes a source reports for one host. It is
// the whole input to [BuildHost], so a caller names each field at the call
// site rather than lining up a row of same-typed arguments.
type HostInput struct {
	IP  string
	MAC string

	// Hostname, when set, is the name the source already knows the host by,
	// such as a DHCP lease name.
	Hostname string

	// ResolveName asks for a reverse DNS lookup when Hostname is empty.
	//
	// Off by default, because a resolved name belongs to whoever resolved it.
	// A sweep asking the local resolver about an address it just probed is
	// reporting its own finding and sets this; a router's ARP table read from
	// elsewhere is not, and filling its nameless rows from this host's resolver
	// would file a name one source invented under another source's claim.
	ResolveName bool

	Interface string
	VLAN      int
}

// BuildHost parses and enriches one host from what a source reported about it.
//
// A malformed address or hardware address is an error. Everything past that is
// best-effort, so a host with no OUI match and no reverse name still builds.
func BuildHost(ctx context.Context, in HostInput) (*Host, error) {
	h := &Host{
		IP:        in.IP,
		MAC:       in.MAC,
		Interface: in.Interface,
		VLAN:      in.VLAN,
		hostname:  in.Hostname,
	}

	if in.IP != "" {
		ipAddr, err := netip.ParseAddr(in.IP)
		if err != nil {
			return nil, fmt.Errorf("parse address %q: %w", in.IP, err)
		}

		h.addr = ipAddr
	}

	// Gated on ResolveName, not just on a missing name: a resolved name belongs
	// to whoever resolved it. See [HostInput.ResolveName].
	if in.ResolveName && h.hostname == "" && h.addr.IsValid() {
		h.hostname = resolveName(ctx, h.addr)
	}

	if in.MAC != "" {
		hw, err := net.ParseMAC(in.MAC)
		if err != nil {
			return nil, fmt.Errorf("parse MAC %q: %w", in.MAC, err)
		}

		h.mac = hw

		// The vendor match and the locally administered bit are read
		// independently: they answer different questions, and an address can
		// have both. Wireshark's manuf file carries a few hundred prefixes a
		// vendor chose without registering, so a device using one is that
		// vendor's hardware whatever its address bit says. A caller after a
		// true privacy address wants [Host.Randomised] and an empty
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

// Address is the parsed IP address, invalid when the source reported none.
func (h Host) Address() netip.Addr {
	return h.addr
}

// Vendor is the full organisation name from the OUI, empty when the address
// matched no registered prefix.
func (h Host) Vendor() string {
	return h.vendor
}

// Hostname is the name the source supplied or the reverse lookup found, empty
// when neither produced one.
func (h Host) Hostname() string {
	return h.hostname
}

// ShortName is the vendor in display form, falling back to the full name and
// then to empty.
func (h Host) ShortName() string {
	return h.shortName
}

// HardwareAddress is the parsed hardware address, nil when the source reported
// none.
func (h Host) HardwareAddress() net.HardwareAddr {
	return h.mac
}

// Randomised reports whether the address has its locally administered bit set.
// See the field of the same name for why that is not "unidentifiable".
func (h Host) Randomised() bool {
	return h.randomised
}

// MarshalJSON writes what a Host was enriched into rather than what it was
// built from. The name, vendor and randomised flag live in unexported fields,
// so the default encoding emits a Host with its useful half missing.
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
