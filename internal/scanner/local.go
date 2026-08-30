package scanner

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// localInterface is the interface holding one of the scanning host's own
// addresses.
type localInterface struct {
	MAC  string
	Name string
}

// localAddrs maps each of this host's addresses to the interface holding it.
// A host never ARPs for its own addresses, so they are absent from the
// neighbour table and the kernel has to be asked directly.
//
// This reads the interfaces of whatever network namespace the process is in,
// so a container on a bridge network sees its own virtual interface rather
// than the host's.
func localAddrs() (map[netip.Addr]localInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}

	out := make(map[netip.Addr]localInterface)

	for _, ifi := range ifaces {
		addrs, err := ifi.Addrs()
		if err != nil {
			// An interface can disappear between being listed and being asked
			// for its addresses, which says nothing about the others.
			continue
		}

		// Loopback and tunnel interfaces have no hardware address, which is a
		// fact about them rather than a failure to read one.
		mac := ""
		if len(ifi.HardwareAddr) > 0 {
			mac = strings.ToLower(ifi.HardwareAddr.String())
		}

		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}

			addr, ok := netip.AddrFromSlice(ipNet.IP)
			if !ok {
				continue
			}

			out[addr.Unmap()] = localInterface{MAC: mac, Name: ifi.Name}
		}
	}

	return out, nil
}
