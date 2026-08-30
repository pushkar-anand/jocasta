// Package cidr enumerates the host addresses of an IPv4 prefix.
package cidr

import (
	"fmt"
	"iter"
	"net/netip"
)

// ErrNotIPv4 is returned for a prefix this package cannot enumerate. IPv6 is
// excluded deliberately: the smallest prefix normally assigned is a /64, so
// walking one address at a time is never the right approach there.
var ErrNotIPv4 = fmt.Errorf("prefix is not IPv4")

// Hosts yields every usable address in p, in ascending order.
//
// For a prefix with a network and a broadcast address, both are skipped: they
// address the network itself rather than a host on it. A /31 is a
// point-to-point link and a /32 is a single host, so neither reserves anything
// and every address in them is yielded.
//
// The sequence is lazily generated and holds no per-address memory, so it can
// be ranged more than once and abandoned early at no cost.
func Hosts(p netip.Prefix) (iter.Seq[netip.Addr], error) {
	first, n, err := bounds(p)
	if err != nil {
		return nil, err
	}

	return func(yield func(netip.Addr) bool) {
		addr := first
		for range n {
			if !yield(addr) {
				return
			}

			addr = addr.Next()
		}
	}, nil
}

// Count reports how many addresses Hosts will yield, without walking them.
func Count(p netip.Prefix) (int, error) {
	_, n, err := bounds(p)

	return n, err
}

// bounds validates p and reduces it to the first host address and a count.
func bounds(p netip.Prefix) (netip.Addr, int, error) {
	if !p.IsValid() {
		return netip.Addr{}, 0, fmt.Errorf("invalid prefix %q", p)
	}

	if !p.Addr().Is4() {
		return netip.Addr{}, 0, fmt.Errorf("%w: %s", ErrNotIPv4, p)
	}

	p = p.Masked()
	total := 1 << (32 - p.Bits())

	if p.Bits() >= 31 {
		return p.Addr(), total, nil
	}

	return p.Addr().Next(), total - 2, nil
}
