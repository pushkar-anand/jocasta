package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"

	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// Networks reads the segments the router serves.
//
// Two tables again: /ip/address knows every prefix and the interface behind
// it, and /interface/vlan is the only place the interface name resolves to a
// tag. The addresses are the answer and the tags decorate it, so losing the
// second costs the tags and not the segments.
func (r *RouterOS) Networks(ctx context.Context) ([]Network, error) {
	addrs, err := r.client.Addresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("read addresses: %w", classifyRouterOS(err))
	}

	var errs []error

	vlans, err := r.client.VLANs(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read vlans: %w", classifyRouterOS(err)))
	}

	return r.buildNetworks(ctx, addrs, byInterface(vlans)), errors.Join(errs...)
}

// byInterface keys the VLAN table the way an address refers to it. A row whose
// tag does not parse is left out, so a lookup that misses reports no tag rather
// than a wrong one.
func byInterface(vlans []routeros.VLAN) map[string]routeros.VLAN {
	out := make(map[string]routeros.VLAN, len(vlans))

	for _, v := range vlans {
		if _, ok := v.Tag(); !ok || v.Name == "" {
			continue
		}

		out[v.Name] = v
	}

	return out
}

// buildNetworks turns the address table into segments.
func (r *RouterOS) buildNetworks(
	ctx context.Context,
	addrs []routeros.IPAddress,
	vlans map[string]routeros.VLAN,
) []Network {
	// Sorted before the walk, so which of two addresses on one prefix names it
	// is the same answer every run rather than however the table came back.
	slices.SortFunc(addrs, func(a, b routeros.IPAddress) int {
		return cmp.Or(
			cmp.Compare(a.Address, b.Address),
			cmp.Compare(a.Interface, b.Interface),
		)
	})

	seen := make(map[netip.Prefix]struct{}, len(addrs))
	out := make([]Network, 0, len(addrs))

	for _, a := range addrs {
		if !a.Usable() {
			continue
		}

		p, ok := segment(a.Address)
		if !ok {
			r.logger.DebugContext(ctx, "ignoring an address that names no segment",
				slog.String("address", a.Address),
				slog.String("interface", a.Interface),
			)

			continue
		}

		if _, dup := seen[p]; dup {
			continue
		}

		seen[p] = struct{}{}

		v, tagged := lookupVLAN(vlans, a)

		n := Network{Prefix: p}
		if tagged {
			n.VLAN, _ = v.Tag()
		}

		// Most specific first: a note on the address describes that prefix, a
		// note on the VLAN describes every prefix on it, and the interface name
		// is what the router calls it when nobody has said anything.
		n.Name = cmp.Or(a.Comment, v.Comment, a.Interface, a.ActualInterface)

		out = append(out, n)
	}

	return out
}

// lookupVLAN finds the tagged interface an address sits on. The configured
// name is tried before the resolved one because that is the name an operator
// wrote, and the resolved one can be the bridge underneath every VLAN.
func lookupVLAN(vlans map[string]routeros.VLAN, a routeros.IPAddress) (routeros.VLAN, bool) {
	for _, name := range []string{a.Interface, a.ActualInterface} {
		if v, ok := vlans[name]; ok {
			return v, true
		}
	}

	return routeros.VLAN{}, false
}

// segment reads the prefix an address sits on, masked to its base.
//
// Loopback and link-local prefixes are dropped: the router holds addresses on
// both and neither is a segment anything is inventoried on.
func segment(s string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, false
	}

	if p.Addr().IsLoopback() || p.Addr().IsLinkLocalUnicast() {
		return netip.Prefix{}, false
	}

	return p.Masked(), true
}

var _ NetworkDiscoverer = (*RouterOS)(nil)
