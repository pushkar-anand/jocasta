// Package oui resolves a MAC address to the organisation that registered it.
//
// The table comes from two sources. The IEEE registries are the authority for
// which prefixes exist. The manuf file of Wireshark gives the short vendor
// names that IEEE does not publish. The table is embedded. So a lookup needs
// no network access and no external database.
//
// A MAC address does not always identify a manufacturer. Current mobile and
// desktop operating systems randomise the address that they show to a
// network. The device makes up this address, so it matches no registry and
// never will. [IsLocallyAdministered] reports this case. Keep it separate from
// a real lookup miss. A random address is expected and permanent. A real miss
// suggests an old table.
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

	// Short is a short form for display, such as "Apple". If no short form
	// is published, Short is Name.
	Short string

	// Bits is the width of the prefix that matched: 24, 28, or 36. A longer
	// prefix is a more specific assignment. It identifies a smaller vendor
	// that bought part of a block from its registrant.
	Bits int
}

// prefixNibbles are the assignment widths that IEEE issues, as hex digit
// counts. Lookup must try them in this order, longest first. A 36-bit
// assignment is inside a 24-bit block that belongs to the reseller. If Lookup
// matched the short prefix first, it would report the reseller.
var prefixNibbles = [...]int{9, 7, 6}

// table is parsed on first use. So a caller that never looks up a vendor does
// not pay to index the embedded data.
var table = sync.OnceValue(load)

// load indexes the embedded vendor records by prefix. It skips malformed rows.
// If a short name is missing, load uses the registered name.
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
// It reports false for an address that is in no registry. This includes every
// locally administered address. See [IsLocallyAdministered].
func Lookup(hw net.HardwareAddr) (Vendor, bool) {
	// The longest assignment is 36 bits, so five bytes decide every lookup.
	const need = 5

	if len(hw) < need {
		return Vendor{}, false
	}

	var buf [need * 2]byte
	hex.Encode(buf[:], hw[:need])

	// hex.Encode writes lowercase, but the table keys are uppercase.
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

// IsLocallyAdministered reports whether the local network assigned hw. The
// second-least-significant bit of the first octet tells this. The address of a
// manufacturer has this bit clear.
//
// Random client addresses set this bit. Virtual interfaces and container
// bridges also set it. Such an address is not a stable identifier. The same
// device shows a different address on another network, and often after it
// connects again. So the address alone cannot identify a device again.
func IsLocallyAdministered(hw net.HardwareAddr) bool {
	if len(hw) == 0 {
		return false
	}

	return hw[0]&0x02 != 0
}
