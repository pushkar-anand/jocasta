package openwrt

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Lease is one active DHCP lease, from dnsmasq for IPv4 or odhcpd for IPv6.
//
// A lease carries a name, which the neighbour table never does, and it
// outlives the device's presence by up to its lease time.
type Lease struct {
	// Address is the leased address. An IPv6 lease can hold several, and
	// each is a lease of its own here.
	Address string

	// MAC is the client's hardware address. An IPv6 lease carries one only
	// when the client's DUID is built from it.
	MAC string

	// Hostname is what the client called itself in its request, or the name
	// a static host gives it. It is empty when the client sent none.
	Hostname string

	// IPv6 marks a lease from DHCPv6.
	IPv6 bool
}

// rawLease is a lease as luci-rpc renders it. IPv4 leases carry ipaddr; IPv6
// leases carry ip6addr and, when they hold more than one, ip6addrs.
type rawLease struct {
	Hostname string   `json:"hostname"`
	MAC      string   `json:"macaddr"`
	IPAddr   string   `json:"ipaddr"`
	IP6Addr  string   `json:"ip6addr"`
	IP6Addrs []string `json:"ip6addrs"`
}

// addresses returns every address the lease holds, each once.
func (r rawLease) addresses() []string {
	out := slices.Clone(r.IP6Addrs)

	for _, a := range []string{r.IPAddr, r.IP6Addr} {
		if a != "" && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}

	return out
}

// DHCPLeases returns the active IPv4 and IPv6 leases. A router without odhcpd
// has no IPv6 leases and says so with an empty list. One family failing does
// not cost the other, so a partial read returns its rows alongside the error.
func (o *OpenWrt) DHCPLeases(ctx context.Context) ([]Lease, error) {
	var (
		out  []Lease
		errs []error
	)

	for _, family := range []int{4, 6} {
		res, err := call[struct {
			V4 []rawLease `json:"dhcp_leases"`
			V6 []rawLease `json:"dhcp6_leases"`
		}](ctx, o, "luci-rpc", "getDHCPLeases", map[string]int{"family": family})
		if err != nil {
			errs = append(errs, fmt.Errorf("leases ipv%d: %w", family, err))

			continue
		}

		for _, r := range append(res.V4, res.V6...) {
			// dnsmasq writes "*" for a client that sent no name.
			name := r.Hostname
			if name == "*" {
				name = ""
			}

			for _, a := range r.addresses() {
				out = append(out, Lease{Address: a, MAC: r.MAC, Hostname: name, IPv6: family == 6})
			}
		}
	}

	return out, errors.Join(errs...)
}

// StaticHost is one host section of /etc/config/dhcp: an address and a name an
// operator bound to a hardware address.
type StaticHost struct {
	// Name is the name the operator gave the host, and is empty when the
	// section binds an address alone.
	Name string

	// MACs are the hardware addresses the section matches. One section can
	// list several, for a device with a wired and a wireless interface.
	MACs []string

	// Address is the IPv4 address the section reserves, and is empty when it
	// reserves none, as a section that only names a host does.
	Address string
}

// StaticHosts returns the host sections of the DHCP configuration.
func (o *OpenWrt) StaticHosts(ctx context.Context) ([]StaticHost, error) {
	sections, err := uciSections(ctx, o, "dhcp", "host")
	if err != nil {
		return nil, err
	}

	out := make([]StaticHost, 0, len(sections))

	// Sorted by section name, so the same configuration reads the same way
	// every time.
	for _, name := range slices.Sorted(maps.Keys(sections)) {
		s := sections[name]

		h := StaticHost{
			Name: first(uciValue(s["name"])),
			MACs: uciValue(s["mac"]),
		}

		// "ignore" tells dnsmasq to refuse the host an address.
		if ip := first(uciValue(s["ip"])); ip != "ignore" {
			h.Address = ip
		}

		out = append(out, h)
	}

	return out, nil
}

// uciValue reads an option as uci renders it: a string for an option, an
// array for a list. An option written as one string can still hold several
// values separated by spaces, as a host's mac often does.
func uciValue(v jsontext.Value) []string {
	if len(v) == 0 {
		return nil
	}

	var list []string
	if err := json.Unmarshal(v, &list); err != nil {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil
		}

		list = []string{s}
	}

	var out []string
	for _, item := range list {
		out = append(out, strings.Fields(item)...)
	}

	return out
}

// first returns the first value, or none.
func first(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return values[0]
}
