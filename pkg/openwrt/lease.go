package openwrt

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Lease is one active DHCP lease, from dnsmasq for IPv4 or odhcpd for IPv6.
//
// A lease has a name, which the neighbour table never has. It can stay for
// up to its lease time after the device goes.
type Lease struct {
	// Address is the leased address. An IPv6 lease can hold several. Here,
	// each address is a separate lease.
	Address string

	// MAC is the hardware address of the client. An IPv6 lease has it only
	// when the DUID of the client comes from it.
	MAC string

	// Hostname is the name that the client gave itself in its request, or
	// the name from a static host. It is empty when the client sent no
	// name.
	Hostname string

	// IPv6 marks a lease from DHCPv6.
	IPv6 bool
}

// rawLease is a lease as luci-rpc renders it. IPv4 leases have ipaddr. IPv6
// leases have ip6addr. If they hold more than one address, they also have
// ip6addrs.
type rawLease struct {
	Hostname string   `json:"hostname"`
	MAC      string   `json:"macaddr"`
	IPAddr   string   `json:"ipaddr"`
	IP6Addr  string   `json:"ip6addr"`
	IP6Addrs []string `json:"ip6addrs"`
}

// addresses returns each address of the lease once.
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
// has no IPv6 leases, and returns an empty list for them. If one family
// fails, the other family still returns. So a partial read returns its rows
// with the error.
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

// StaticHost is one host section of /etc/config/dhcp. It is an address and a
// name that an operator bound to a hardware address.
type StaticHost struct {
	// Name is the name that the operator gave the host. It is empty when the
	// section binds only an address.
	Name string

	// MACs are the hardware addresses that the section matches. One section
	// can list several, for a device with a wired and a wireless interface.
	MACs []string

	// Address is the IPv4 address that the section reserves. It is empty when
	// the section reserves no address and only names a host.
	Address string
}

// StaticHosts returns the host sections of the DHCP configuration, in the
// order of the file.
func (o *OpenWrt) StaticHosts(ctx context.Context) ([]StaticHost, error) {
	sections, err := uciSections(ctx, o, "dhcp", "host")
	if err != nil {
		return nil, err
	}

	out := make([]StaticHost, 0, len(sections))

	for _, s := range sections {
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
// array for a list. An option as one string can still hold several values,
// separated by spaces. The mac of a host often does this.
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
