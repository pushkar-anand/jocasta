// Package oui resolves a MAC address to the organisation that registered it.
//
// The table is built from the IEEE registries, which are authoritative for
// which prefixes exist, and from Wireshark's manuf file, which supplies the
// abbreviated vendor names that IEEE does not publish. It is embedded, so a
// lookup needs no network access and no external database.
//
// A MAC address does not always identify a manufacturer. Current mobile and
// desktop operating systems randomise the address they present to a network,
// and a randomised address is generated rather than assigned, so it matches no
// registry and never will. [IsLocallyAdministered] reports that case, which is
// worth distinguishing from a genuine lookup miss: one is expected and
// permanent, the other suggests a stale table.
package oui

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/hex"
	"net"
	"strings"
	"sync"
)

//go:embed data.txt
var data []byte

// Vendor is a registered organisation.
type Vendor struct {
	// Name is the organisation as registered, such as "Apple, Inc.".
	Name string

	// Short is an abbreviated form suitable for display, such as "Apple".
	// It falls back to Name where no abbreviation is published.
	Short string

	// Bits is the width of the prefix that matched: 24, 28, or 36. A narrower
	// match is a more specific assignment and identifies a smaller vendor
	// buying part of a block rather than the block's registrant.
	Bits int
}

// prefixNibbles are the assignment widths IEEE issues, as hex digit counts, in
// the order they must be tried. Longest first: a 36-bit assignment sits inside
// a 24-bit block registered to whoever resells it, so matching the short
// prefix first would report the reseller instead of the vendor.
var prefixNibbles = [...]int{9, 7, 6}

// table is parsed on first use. Decompressing and indexing sixty thousand
// entries costs a few milliseconds, which a caller that never looks up a
// vendor should not pay.
var table = sync.OnceValue(load)

func load() map[string]Vendor {
	m := make(map[string]Vendor, 64_000)

	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue
		}

		short, long, ok := strings.Cut(rest, "\t")
		if !ok {
			continue
		}

		if short == "" {
			short = long
		}

		m[key] = Vendor{Name: long, Short: short, Bits: len(key) * 4}
	}

	return m
}

// Lookup returns the organisation that registered hw.
//
// It reports false for an address in no registry, which includes every
// locally administered address; see [IsLocallyAdministered].
func Lookup(hw net.HardwareAddr) (Vendor, bool) {
	// The longest assignment is 36 bits, so five bytes decide every lookup.
	const need = 5

	if len(hw) < need {
		return Vendor{}, false
	}

	var buf [need * 2]byte
	hex.Encode(buf[:], hw[:need])

	// hex.Encode emits lowercase; the table is keyed uppercase.
	for i, c := range buf {
		if c >= 'a' {
			buf[i] = c - 'a' + 'A'
		}
	}

	// Converting a byte slice for a map lookup does not allocate.
	m := table()
	for _, n := range prefixNibbles {
		if v, ok := m[string(buf[:n])]; ok {
			return v, true
		}
	}

	return Vendor{}, false
}

// IsLocallyAdministered reports whether hw was assigned by the local network
// rather than by a manufacturer, which is what the second-least-significant
// bit of the first octet means.
//
// Randomised client addresses set this bit, as do virtual interfaces and
// container bridges. Such an address is not a stable identifier: the same
// device presents a different one on another network, and often after a
// reconnection, so it cannot be used on its own to recognise a device again.
func IsLocallyAdministered(hw net.HardwareAddr) bool {
	if len(hw) == 0 {
		return false
	}

	return hw[0]&0x02 != 0
}
