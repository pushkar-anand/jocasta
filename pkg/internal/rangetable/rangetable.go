// Package rangetable maps address ranges to values for packages asn and geo.
// Both packages embed a gzipped list of range starts. They look up an address
// by the last start at or below it.
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

// Table holds the range starts of each family, in address order, with the
// value of each range.
type Table[V any] struct {
	v4Start []uint32
	v4      []V
	v6Start []v6
	v6      []V
}

// v6 is an IPv6 address as two big-endian halves. The halves compare in
// address order.
type v6 struct{ hi, lo uint64 }

func toV6(a netip.Addr) v6 {
	b := a.As16()

	return v6{binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])}
}

func (a v6) compare(b v6) int {
	return cmp.Or(cmp.Compare(a.hi, b.hi), cmp.Compare(a.lo, b.lo))
}

// Add appends a range that begins at start. In each family, the caller must
// add ranges in address order.
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

// Lookup returns the value of the range that contains addr. This is the range
// with the last start at or below addr. It reports false when addr is below
// every start.
func (t *Table[V]) Lookup(addr netip.Addr) (V, bool) {
	if addr.Is4() {
		b := addr.As4()

		return floor(t.v4Start, t.v4, binary.BigEndian.Uint32(b[:]), cmp.Compare[uint32])
	}

	return floor(t.v6Start, t.v6, toV6(addr), v6.compare)
}

// floor returns the value of the last start at or below key.
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
// at build time. So a corrupt table means a bad build. EachLine then yields no
// lines, and every lookup misses.
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
