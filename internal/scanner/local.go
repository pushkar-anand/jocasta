package scanner

import (
	"fmt"
	"net"
	"net/netip"
)

// localInterface is the interface that has one of the own addresses of the
// scanning host.
type localInterface struct {
	MAC  string
	Name string
}

// localAddrs maps each address of this host to the interface that has it. A
// host never ARPs for its own addresses. So they are not in the neighbour
// table, and the code must ask the kernel directly.
//
// This reads the interfaces of the network namespace of the process. So a
// container on a bridge network sees its own virtual interface.
func localAddrs() (map[netip.Addr]localInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}

	out := make(map[netip.Addr]localInterface)

	for _, ifi := range ifaces {
		addrs, err := ifi.Addrs()
		if err != nil {
			// An interface can go away between the list and the request for
			// its addresses. This tells nothing about the others.
			continue
		}

		// Loopback and tunnel interfaces have no hardware address. So an empty
		// address is normal there.
		mac := ""
		if len(ifi.HardwareAddr) > 0 {
			mac = ifi.HardwareAddr.String()
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
