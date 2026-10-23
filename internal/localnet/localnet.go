// Package localnet finds the network of the host that Jocasta runs on.
package localnet

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

// probe is an address outside every local network, so the kernel routes it
// through the default route.
const probe = "192.0.2.1:9"

// Current returns the IPv4 network that the host's default route uses, such as
// 192.0.2.0/24. It returns an error if the host has no IPv4 default route.
func Current(ctx context.Context) (netip.Prefix, error) {
	// A UDP dial sends no packet. The kernel only picks the source address
	// that it would use for the probe.
	var d net.Dialer

	conn, err := d.DialContext(ctx, "udp4", probe)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("find the default route: %w", err)
	}

	defer func() { _ = conn.Close() }()

	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Prefix{}, fmt.Errorf("unexpected local address %s", conn.LocalAddr())
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("list interface addresses: %w", err)
	}

	return prefixOf(local.AddrPort().Addr().Unmap(), addrs)
}

// prefixOf returns the network of the interface address in addrs that equals a.
func prefixOf(a netip.Addr, addrs []net.Addr) (netip.Prefix, error) {
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		ip, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok || ip.Unmap() != a {
			continue
		}

		ones, _ := ipNet.Mask.Size()

		return netip.PrefixFrom(a, ones).Masked(), nil
	}

	return netip.Prefix{}, fmt.Errorf("no interface has the address %s", a)
}
