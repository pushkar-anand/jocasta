package scanner

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/hosts"
)

// procNetARP is the IPv4 neighbour table of the kernel. A read of it costs
// nothing and needs no privileges. To send ARP requests directly would need
// privileges.
const procNetARP = "/proc/net/arp"

// arpFlagComplete (ATF_COM) marks an entry whose hardware address is really
// resolved. Incomplete entries have an all-zero MAC and mean nothing.
const arpFlagComplete = 0x2

// neighbours maps on-link IPv4 addresses to their hardware addresses. On a
// system without a neighbour table, it returns an empty map and a nil error.
// A host with no MAC is a normal result of a scan.
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

// parseARP reads the neighbour table format. Each line is one host: an
// address, a hardware type, flags, a MAC, a mask and a device. One header
// line is at the top.
func parseARP(r io.Reader) (map[netip.Addr]string, error) {
	out := make(map[netip.Addr]string)

	sc := bufio.NewScanner(r)
	for lineNo := 0; sc.Scan(); lineNo++ {
		if lineNo == 0 {
			continue
		}

		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}

		addr, err := netip.ParseAddr(fields[0])
		if err != nil {
			continue
		}

		// Base 0 reads the 0x prefix of these values in the table.
		flags, err := strconv.ParseUint(fields[2], 0, 32)
		if err != nil || flags&arpFlagComplete == 0 {
			continue
		}

		mac, ok := hosts.CanonicalMAC(fields[3])
		if !ok || mac == "" {
			continue
		}

		out[addr] = mac
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read neighbour table: %w", err)
	}

	return out, nil
}
