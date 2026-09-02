// Package hosts has types and utilities for inspecting, resolving,
// and enriching network host metadata such as IP addresses, MAC addresses,
// vendor lookups, and reverse DNS hostnames.
package hosts

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"github.com/pushkar-anand/jocasta/pkg/oui"
)

// Host represents a discovered network host containing both raw input
// attributes and enriched metadata (parsed addresses, vendor info, reverse DNS).
type Host struct {
	// IP is the raw string representation of the device's IP address.
	IP string

	// MAC is the raw string representation of the hardware (MAC) address.
	// Note that this can be a locally administered or randomised identifier.
	MAC string

	// Interface is the identifier or name of the network interface the device is seen on.
	Interface string

	// VLAN is the Virtual LAN identifier associated with the host.
	VLAN int

	// addr holds the parsed, type-safe IP address representation.
	addr netip.Addr

	// mac holds the parsed, type-safe hardware address representation.
	mac net.HardwareAddr

	// hostname is the reverse DNS hostname of the device, if resolved.
	hostname string

	// vendor is the organisation name registered to the MAC address prefix (OUI) in the IEEE database.
	vendor string

	// shortName is a standardised or commonly recognised brand name/abbreviation for the vendor.
	shortName string

	// randomised indicates whether the MAC address is locally administered (randomised),
	// a common privacy measure on modern mobile devices and laptops to mitigate tracking.
	randomised bool
}

// BuildHost constructs and enriches a Host instance from the supplied network parameters.
// It parses the IP and MAC addresses into structured types, performs reverse DNS resolution
// if no explicit hostname is provided, and enriches MAC metadata via OUI lookup.
func BuildHost(
	ctx context.Context,
	ip string,
	MAC string,
	hostname string,
	interfaceName string,
	vlan int,
) (*Host, error) {
	h := &Host{
		IP:        ip,
		MAC:       MAC,
		Interface: interfaceName,
		VLAN:      vlan,
		hostname:  hostname,
	}

	if ip != "" {
		ipAddr, err := netip.ParseAddr(ip)
		if err != nil {
			return nil, fmt.Errorf("parse address %q: %w", ip, err)
		}

		h.addr = ipAddr
	}

	// Attempt to reverse DNS resolution if no explicit hostname was supplied.
	if h.hostname == "" && h.addr.IsValid() {
		h.hostname = resolveName(ctx, h.addr)
	}

	if MAC != "" {
		hw, err := net.ParseMAC(h.MAC)
		if err != nil {
			return nil, fmt.Errorf("parse MAC %q: %w", h.MAC, err)
		}

		h.mac = hw

		info, found := oui.Lookup(hw)
		if found {
			h.shortName = info.Short
			h.vendor = info.Name
		}

		if oui.IsLocallyAdministered(hw) {
			h.randomised = true
		}
	}

	return h, nil
}

// Address returns the parsed IP address as a netip.Addr.
func (h Host) Address() netip.Addr {
	return h.addr
}

// Vendor returns the organisation registered to the MAC OUI prefix.
func (h Host) Vendor() string {
	return h.vendor
}

// Hostname returns the host's resolved or assigned domain name.
func (h Host) Hostname() string {
	return h.hostname
}

// ShortName returns the shortened or canonical brand name for the hardware vendor.
func (h Host) ShortName() string {
	return h.shortName
}

// HardwareAddress returns the parsed MAC address as a net.HardwareAddr.
func (h Host) HardwareAddress() net.HardwareAddr {
	return h.mac
}

// Randomised reports whether the hardware address has its locally administered bit set.
func (h Host) Randomised() bool {
	return h.randomised
}
