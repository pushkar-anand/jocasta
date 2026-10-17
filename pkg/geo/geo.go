// Package geo resolves an internet address to the country where it is
// registered. So the traffic map can put the peers of the network on a map of
// the world.
//
// The table comes from the IP to Country Lite database of DB-IP. Its licence
// is CC BY 4.0, which requires a credit wherever its countries show. Use the
// line in [Attribution]. The table is embedded. So a lookup needs no network
// access and no external database.
//
// A country is where the address is registered. A provider often registers
// its addresses in its home country, but the server can be near you. CDN
// addresses are where the CDN edges are. So the country tells whose network it
// is, and not only where it is.
package geo

import (
	_ "embed"
	"net/netip"
	"strings"
	"sync"

	"github.com/pushkar-anand/jocasta/pkg/asn"
	"github.com/pushkar-anand/jocasta/pkg/internal/rangetable"
)

// Attribution is the credit that the licence of DB-IP requires wherever these
// countries show.
const Attribution = "IP to Country data by DB-IP (db-ip.com), CC BY 4.0"

// unknown is the DB-IP code for address space that no country holds.
const unknown = "ZZ"

//go:embed ranges.gz
var rangesData []byte

// tables are parsed on first use, as in package asn. A caller that never looks
// up an address must not pay for the index.
var tables = sync.OnceValue(load)

// pack keeps a two-letter code in two bytes.
func pack(code string) uint16 { return uint16(code[0])<<8 | uint16(code[1]) }

func unpack(c uint16) string { return string([]byte{byte(c >> 8), byte(c & 0xff)}) }

// load builds the embedded address-to-country table. It skips rows that do not
// have a valid address and a two-letter country code.
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

// Lookup returns the two-letter ISO 3166 code of the country where addr is
// registered, such as "AU".
//
// It reports false for an address that package asn finds is not on the public
// internet. It also reports false for an address that no country holds.
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
