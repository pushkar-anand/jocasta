// Package rangetable maps address ranges to values for packages asn and geo,
// which both embed a gzipped list of range starts and look an address up by
// the last start at or below it.
package rangetable

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"encoding/binary"
	"net/netip"
	"slices"
)

// Table holds each family's range starts, in address order, beside the value
// each range carries.
type Table[V any] struct {
	v4Start []uint32
	v4      []V
	v6Start []v6
	v6      []V
}

// v6 is an IPv6 address as two big-endian halves, which compare in address
// order.
type v6 struct{ hi, lo uint64 }

func toV6(a netip.Addr) v6 {
	b := a.As16()

	return v6{binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])}
}

func (a v6) compare(b v6) int {
	return cmp.Or(cmp.Compare(a.hi, b.hi), cmp.Compare(a.lo, b.lo))
}

// Add appends a range starting at start. Ranges must be added in address
// order within each family.
func (t *Table[V]) Add(start netip.Addr, v V) {
	if start.Is4() {
		b := start.As4()
		t.v4Start = append(t.v4Start, binary.BigEndian.Uint32(b[:]))
		t.v4 = append(t.v4, v)

		return
	}

	t.v6Start = append(t.v6Start, toV6(start))
	t.v6 = append(t.v6, v)
}

// Lookup returns the value of the range addr falls in: the one whose start is
// the last at or below it. It reports false when addr is below every start.
func (t *Table[V]) Lookup(addr netip.Addr) (V, bool) {
	if addr.Is4() {
		b := addr.As4()

		return floor(t.v4Start, t.v4, binary.BigEndian.Uint32(b[:]), cmp.Compare[uint32])
	}

	return floor(t.v6Start, t.v6, toV6(addr), v6.compare)
}

// floor returns the value paired with the last start at or below key.
func floor[K, V any](starts []K, values []V, key K, compare func(K, K) int) (V, bool) {
	i, found := slices.BinarySearchFunc(starts, key, compare)
	if !found {
		i--
	}

	if i < 0 {
		var zero V

		return zero, false
	}

	return values[i], true
}

// EachLine calls fn with every line of the gzipped text gz. Tables are embedded
// at build time, so a corrupt one is a build that should never have shipped;
// it yields no lines, and every lookup misses.
func EachLine(gz []byte, fn func(string)) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return
	}

	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		fn(sc.Text())
	}
}
