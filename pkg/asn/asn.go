// Package asn resolves an internet address to the organisation that announces
// it, so an address can be shown as "Amazon".
//
// The tables are built from DB-IP's IP to ASN Lite database, which is licensed
// under CC BY 4.0 and must be credited wherever its names are shown:
// [Attribution] is the line to use. They are embedded, so a lookup needs no
// network access and no external database.
//
// An ASN names the network that routes an address. A site hosted on a cloud
// provider resolves to the provider, and a CDN edge resolves to the CDN. That
// still answers "who is this device talking to", one level up from the answer
// a person might guess.
package asn

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Attribution is the credit DB-IP's licence requires wherever these names are
// shown.
const Attribution = "IP to ASN data by DB-IP (db-ip.com), CC BY 4.0"

//go:embed ranges.gz
var rangesData []byte

//go:embed orgs.gz
var orgsData []byte

// Org is an organisation that announces addresses.
type Org struct {
	// ASN is the autonomous system number, the stable identity of the network.
	ASN uint32

	// Name is the organisation as registered, such as "Amazon.com, Inc.".
	Name string

	// Short is an abbreviated form suitable for display, such as "Amazon".
	// It falls back to Name where nothing could be dropped.
	Short string
}

// tables are parsed on first use. Decompressing and indexing half a million
// ranges takes a noticeable fraction of a second, which a caller that never
// looks up an address should not pay.
var tables = sync.OnceValue(load)

type index struct {
	v4Start []uint32
	v4ASN   []uint32
	v6Start []v6
	v6ASN   []uint32
	orgs    map[uint32]Org
}

// v6 is an IPv6 address as two big-endian halves, which compare in address
// order.
type v6 struct{ hi, lo uint64 }

func toV6(a netip.Addr) v6 {
	b := a.As16()

	return v6{binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])}
}

func (a v6) compare(b v6) int {
	if c := cmp.Compare(a.hi, b.hi); c != 0 {
		return c
	}

	return cmp.Compare(a.lo, b.lo)
}

func load() *index {
	ix := &index{orgs: make(map[uint32]Org, 90_000)}

	eachLine(rangesData, func(line string) {
		addr, asnField, ok := strings.Cut(line, "\t")
		if !ok {
			return
		}

		a, err := netip.ParseAddr(addr)
		if err != nil {
			return
		}

		n, err := strconv.ParseUint(asnField, 10, 32)
		if err != nil {
			return
		}

		if a.Is4() {
			b := a.As4()
			ix.v4Start = append(ix.v4Start, binary.BigEndian.Uint32(b[:]))
			ix.v4ASN = append(ix.v4ASN, uint32(n))

			return
		}

		ix.v6Start = append(ix.v6Start, toV6(a))
		ix.v6ASN = append(ix.v6ASN, uint32(n))
	})

	eachLine(orgsData, func(line string) {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			return
		}

		n, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil {
			return
		}

		short := cmp.Or(fields[1], fields[2])
		ix.orgs[uint32(n)] = Org{ASN: uint32(n), Name: fields[2], Short: short}
	})

	return ix
}

func eachLine(gz []byte, fn func(string)) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		// The data is embedded at build time; a corrupt table is a build that
		// should never have shipped, and every lookup misses.
		return
	}

	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		fn(sc.Text())
	}
}

// Lookup returns the organisation that announces addr.
//
// It reports false for an address that is not on the public internet
// (private, loopback, link-local, shared, documentation and multicast
// ranges), and for a public address no organisation announces.
func Lookup(addr netip.Addr) (Org, bool) {
	addr = addr.Unmap()
	if !IsPublic(addr) {
		return Org{}, false
	}

	ix := tables()

	var asn uint32

	if addr.Is4() {
		b := addr.As4()
		asn, _ = floor(ix.v4Start, ix.v4ASN, binary.BigEndian.Uint32(b[:]), cmp.Compare[uint32])
	} else {
		asn, _ = floor(ix.v6Start, ix.v6ASN, toV6(addr), v6.compare)
	}

	if asn == 0 {
		return Org{}, false
	}

	if org, ok := ix.orgs[asn]; ok {
		return org, true
	}

	// Announced, but by a network the source gave no name.
	return Org{ASN: asn, Name: "AS" + strconv.FormatUint(uint64(asn), 10), Short: "AS" + strconv.FormatUint(uint64(asn), 10)}, true
}

// floor returns the value paired with the last start at or below key.
func floor[K any](starts []K, values []uint32, key K, compare func(K, K) int) (uint32, bool) {
	i, found := slices.BinarySearchFunc(starts, key, compare)
	if !found {
		i--
	}

	if i < 0 {
		return 0, false
	}

	return values[i], true
}

// nonPublic are the special-purpose ranges that netip does not already
// classify: shared address space for carrier-grade NAT, benchmarking, the
// documentation ranges, and the IPv4 block reserved for future use.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// IsPublic reports whether addr is an address on the public internet, the only
// kind worth looking up. No organisation announces a private address.
func IsPublic(addr netip.Addr) bool {
	addr = addr.Unmap()

	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}

	for _, p := range nonPublic {
		if p.Contains(addr) {
			return false
		}
	}

	return true
}
