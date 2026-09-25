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
	_ "embed"
	"net/netip"
	"strings"
	"sync"

	"github.com/pushkar-anand/jocasta/pkg/asn"
	"github.com/pushkar-anand/jocasta/pkg/internal/rangetable"
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

// pack keeps a two-letter code in two bytes.
func pack(code string) uint16 { return uint16(code[0])<<8 | uint16(code[1]) }

func unpack(c uint16) string { return string([]byte{byte(c >> 8), byte(c & 0xff)}) }

func load() *rangetable.Table[uint16] {
	t := &rangetable.Table[uint16]{}

	rangetable.EachLine(rangesData, func(line string) {
		addr, code, ok := strings.Cut(line, "\t")
		if !ok || len(code) != 2 {
			return
		}

		a, err := netip.ParseAddr(addr)
		if err != nil {
			return
		}

		t.Add(a, pack(code))
	})

	return t
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

	code, ok := tables().Lookup(addr)
	if !ok || unpack(code) == unknown {
		return "", false
	}

	return unpack(code), true
}
