package scanner

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// procNetARP is the kernel's IPv4 neighbour table. Reading it is free and needs
// no privileges, unlike sending ARP requests directly.
const procNetARP = "/proc/net/arp"

// arpFlagComplete (ATF_COM) marks an entry whose hardware address is actually
// resolved. Incomplete entries carry an all-zero MAC and mean nothing.
const arpFlagComplete = 0x2

// zeroMAC is what an unresolved entry carries.
const zeroMAC = "00:00:00:00:00:00"

// neighbours maps on-link IPv4 addresses to their hardware addresses. On a
// system without a neighbour table to read it returns an empty map rather than
// an error: no MAC is a normal outcome, not a failed scan.
func neighbours() (map[netip.Addr]string, error) {
	f, err := os.Open(procNetARP)
	if err != nil {
		if os.IsNotExist(err) {
			return map[netip.Addr]string{}, nil
		}

		return nil, fmt.Errorf("open %s: %w", procNetARP, err)
	}
	defer func() { _ = f.Close() }()

	return parseARP(f)
}

// parseARP reads the neighbour table format: an address, a hardware type, flags,
// a MAC, a mask and a device, one host per line under a single header line.
func parseARP(r io.Reader) (map[netip.Addr]string, error) {
	out := make(map[netip.Addr]string)

	scanner := bufio.NewScanner(r)
	for lineNo := 0; scanner.Scan(); lineNo++ {
		if lineNo == 0 {
			continue
		}

		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}

		addr, err := netip.ParseAddr(fields[0])
		if err != nil {
			continue
		}

		// Base 0 reads the 0x prefix the table writes these in.
		flags, err := strconv.ParseUint(fields[2], 0, 32)
		if err != nil || flags&arpFlagComplete == 0 {
			continue
		}

		// ParseMAC rejects a malformed address and its String normalises the
		// separator and case, so entries are comparable however they were written.
		hw, err := net.ParseMAC(fields[3])
		if err != nil {
			continue
		}

		mac := hw.String()
		if mac == zeroMAC {
			continue
		}

		out[addr] = mac
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read neighbour table: %w", err)
	}

	return out, nil
}
