// Package geo resolves an internet address to the country it is registered
// in, so the traffic map can place what the network talks to on a map of the
// world.
//
// The table is built from DB-IP's IP to Country Lite database, which is
// licensed under CC BY 4.0 and must be credited wherever its countries are
// shown: [Attribution] is the line to use. It is embedded, so a lookup needs
// no network access and no external database.
//
// A country is where the address is registered. A provider's addresses are
// often registered in its home country while the server is next door, and a
// CDN's are wherever its edges are, so the country says whose network it is as
// much as where.
package geo

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"github.com/pushkar-anand/jocasta/pkg/asn"
)

// Attribution is the credit DB-IP's licence requires wherever these countries
// are shown.
const Attribution = "IP to Country data by DB-IP (db-ip.com), CC BY 4.0"

// unknown is DB-IP's code for space no country holds.
const unknown = "ZZ"

//go:embed ranges.gz
var rangesData []byte

// tables are parsed on first use, as package asn's are: a caller that never
// looks up an address should not pay for indexing the table.
var tables = sync.OnceValue(load)

type index struct {
	v4Start []uint32
	v4Code  []uint16
	v6Start []v6
	v6Code  []uint16
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

// pack keeps a two-letter code in two bytes.
func pack(code string) uint16 { return uint16(code[0])<<8 | uint16(code[1]) }

func unpack(c uint16) string { return string([]byte{byte(c >> 8), byte(c & 0xff)}) }

func load() *index {
	ix := &index{}

	zr, err := gzip.NewReader(bytes.NewReader(rangesData))
	if err != nil {
		// The data is embedded at build time; a corrupt table is a build that
		// should never have shipped, and every lookup misses.
		return ix
	}

	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		addr, code, ok := strings.Cut(sc.Text(), "\t")
		if !ok || len(code) != 2 {
			continue
		}

		a, err := netip.ParseAddr(addr)
		if err != nil {
			continue
		}

		if a.Is4() {
			b := a.As4()
			ix.v4Start = append(ix.v4Start, binary.BigEndian.Uint32(b[:]))
			ix.v4Code = append(ix.v4Code, pack(code))

			continue
		}

		ix.v6Start = append(ix.v6Start, toV6(a))
		ix.v6Code = append(ix.v6Code, pack(code))
	}

	return ix
}

// Lookup returns the two-letter ISO 3166 code of the country addr is
// registered in, such as "AU".
//
// It reports false for an address that is not on the public internet, as
// package asn judges it, and for one no country holds.
func Lookup(addr netip.Addr) (string, bool) {
	addr = addr.Unmap()
	if !asn.IsPublic(addr) {
		return "", false
	}

	ix := tables()

	var (
		code uint16
		ok   bool
	)

	if addr.Is4() {
		b := addr.As4()
		code, ok = floor(ix.v4Start, ix.v4Code, binary.BigEndian.Uint32(b[:]), cmp.Compare[uint32])
	} else {
		code, ok = floor(ix.v6Start, ix.v6Code, toV6(addr), v6.compare)
	}

	if !ok || unpack(code) == unknown {
		return "", false
	}

	return unpack(code), true
}

// floor returns the value paired with the last start at or below key.
func floor[K any](starts []K, values []uint16, key K, compare func(K, K) int) (uint16, bool) {
	i, found := slices.BinarySearchFunc(starts, key, compare)
	if !found {
		i--
	}

	if i < 0 {
		return 0, false
	}

	return values[i], true
}
