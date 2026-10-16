// Package cidr enumerates the host addresses of an IPv4 prefix.
package cidr

import (
	"errors"
	"fmt"
	"iter"
	"net/netip"
)

// ErrNotIPv4 is returned for a prefix that is not IPv4. The package does not
// support IPv6 on purpose. The smallest IPv6 prefix in normal use is a /64,
// and it is too large to walk one address at a time.
var ErrNotIPv4 = errors.New("prefix is not IPv4")

// Hosts yields every usable address in p, in ascending order.
//
// Hosts skips the network and broadcast addresses, because they address the
// network itself. A /31 is a point-to-point link, and a /32 is one host.
// Neither reserves an address, so Hosts yields every address in them.
//
// The sequence is lazy and keeps no memory for each address. So a caller can
// range over it more than once, and stop early at no cost.
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

// bounds validates p and returns its first host address and a count.
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
