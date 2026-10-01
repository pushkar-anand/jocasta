package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"

	"github.com/pushkar-anand/jocasta/pkg/openwrt"
)

// Networks reads the segments the router serves.
//
// The interfaces netifd reports are the answer, and the VLAN devices in the
// network configuration decorate them with tags, so losing the second costs
// only the tags.
func (o *OpenWrt) Networks(ctx context.Context) ([]Network, error) {
	ifaces, err := o.client.Interfaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("read interfaces: %w", classifyOpenWrt(err))
	}

	var errs []error

	vlans, err := o.client.VLANs(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read vlans: %w", classifyOpenWrt(err)))
	}

	return buildOpenWrtNetworks(ifaces, vlans), errors.Join(errs...)
}

// buildOpenWrtNetworks turns the interfaces into segments.
//
// A segment is a prefix on an interface that is up and configured static. An
// uplink, whose protocol is dhcp, pppoe or the like, holds an address on a
// segment the router does not serve. IPv6 counts the prefixes assigned to the
// interface as well, which is how a delegated prefix reaches a LAN.
func buildOpenWrtNetworks(ifaces []openwrt.Interface, vlans map[string]int) []Network {
	// Sorted before the walk, so which of two interfaces on one prefix names
	// it is the same answer every run.
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
