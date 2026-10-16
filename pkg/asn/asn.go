// Package asn resolves an internet address to the organisation that announces
// it. So the UI can show an address as "Amazon".
//
// The tables come from the IP to ASN Lite database of DB-IP. Its licence is
// CC BY 4.0, which requires a credit wherever its names show. Use the line in
// [Attribution]. The tables are embedded. So a lookup needs no network access
// and no external database.
//
// An ASN names the network that routes an address. A site on a cloud provider
// resolves to the provider, and a CDN edge resolves to the CDN. This still
// tells who the device talks to, but one level above the answer that a person
// can expect.
package asn

import (
	"cmp"
	_ "embed"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/pushkar-anand/jocasta/pkg/internal/rangetable"
)

// Attribution is the credit that the licence of DB-IP requires wherever these
// names show.
const Attribution = "IP to ASN data by DB-IP (db-ip.com), CC BY 4.0"

//go:embed ranges.gz
var rangesData []byte

//go:embed orgs.gz
var orgsData []byte

// Org is an organisation that announces addresses.
type Org struct {
	// ASN is the autonomous system number. It is the stable identity of the
	// network.
	ASN uint32

	// Name is the organisation as registered, such as "Amazon.com, Inc.".
	Name string

	// Short is a short form for display, such as "Amazon". If no part of Name
	// can be removed, Short is Name.
	Short string
}

// tables are parsed on first use. To decompress and index half a million
// ranges takes a large part of a second. A caller that never looks up an
// address must not pay this cost.
var tables = sync.OnceValue(load)

type index struct {
	ranges rangetable.Table[uint32]
	orgs   map[uint32]Org
}

// load builds the embedded address and organisation index. It skips rows with
// fields that do not parse.
func load() *index {
	ix := &index{orgs: make(map[uint32]Org, 90_000)}

	rangetable.EachLine(rangesData, func(line string) {
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

		ix.ranges.Add(a, uint32(n))
	})

	rangetable.EachLine(orgsData, func(line string) {
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

// Lookup returns the organisation that announces addr.
//
// It reports false for an address that is not on the public internet. These
// are the private, loopback, link-local, shared, documentation and multicast
// ranges. It also reports false for a public address that no organisation
// announces.
func Lookup(addr netip.Addr) (Org, bool) {
	addr = addr.Unmap()
	if !IsPublic(addr) {
		return Org{}, false
	}

	ix := tables()

	asn, _ := ix.ranges.Lookup(addr)

	if asn == 0 {
		return Org{}, false
	}

	if org, ok := ix.orgs[asn]; ok {
		return org, true
	}

	// A network announces the address, but the source gives the network no
	// name.
	return Org{ASN: asn, Name: "AS" + strconv.FormatUint(uint64(asn), 10), Short: "AS" + strconv.FormatUint(uint64(asn), 10)}, true
}

// nonPublic are the special-purpose ranges that netip does not classify:
//   - shared address space for carrier-grade NAT
//   - benchmarking
//   - the documentation ranges
//   - the IPv4 block reserved for future use
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

// IsPublic reports whether addr is an address on the public internet. Only
// these addresses are worth a lookup. No organisation announces a private
// address.
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
