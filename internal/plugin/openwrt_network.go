package plugin

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"

	"github.com/pushkar-anand/jocasta/pkg/openwrt"
)

// Networks reads the segments that the router serves.
//
// The interfaces that netifd reports are the answer. The VLAN devices in the
// network configuration add tags to them.
func (o *OpenWrt) Networks(ctx context.Context) ([]Network, error) {
	ifaces, err := o.client.Interfaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("read interfaces: %w", classifyOpenWrt(err))
	}

	vlans, err := o.client.VLANs(ctx)
	if err != nil {
		return nil, fmt.Errorf("read vlans: %w", classifyOpenWrt(err))
	}

	return buildOpenWrtNetworks(ifaces, vlans), nil
}

// buildOpenWrtNetworks turns the interfaces into segments.
//
// A segment is a prefix on an interface that is up and configured static. An
// uplink, with the protocol dhcp, pppoe or similar, has an address on a
// segment that the router does not serve. For IPv6, it also counts the
// prefixes assigned to the interface. A delegated prefix reaches a LAN in this
// way.
func buildOpenWrtNetworks(ifaces []openwrt.Interface, vlans map[string]int) []Network {
	// Sort before the walk. So if two interfaces are on one prefix, the same
	// interface names it on every run.
	ifaces = slices.Clone(ifaces)
	slices.SortFunc(ifaces, func(a, b openwrt.Interface) int { return cmp.Compare(a.Name, b.Name) })

	seen := make(map[netip.Prefix]struct{})

	var out []Network

	for _, i := range ifaces {
		if !i.Up {
			continue
		}

		var prefixes []string

		if i.Proto == openwrt.ProtoStatic {
			for _, a := range slices.Concat(i.IPv4, i.IPv6) {
				prefixes = append(prefixes, a.Address+"/"+strconv.Itoa(a.Mask))
			}
		}

		for _, a := range i.Assigned {
			prefixes = append(prefixes, a.Address+"/"+strconv.Itoa(a.Mask))
		}

		vlan := cmp.Or(vlans[i.L3Device], vlans[i.Device])

		for _, raw := range prefixes {
			p, ok := segment(raw)
			if !ok {
				continue
			}

			if _, dup := seen[p]; dup {
				continue
			}

			seen[p] = struct{}{}

			out = append(out, Network{Prefix: p, Name: i.Name, VLAN: vlan})
		}
	}

	return out
}

var _ NetworkDiscoverer = (*OpenWrt)(nil)
