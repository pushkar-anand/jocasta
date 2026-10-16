package inventory

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// Broadcast is what one device sent to everyone on one group and port, over a
// window.
type Broadcast struct {
	// Dst is the multicast group, 255.255.255.255, or the broadcast address of
	// the subnet. Kind tells which of the three.
	Dst  netip.Addr `json:"dst"`
	Kind string     `json:"kind"`

	Protocol uint8  `json:"protocol"`
	Port     uint16 `json:"port"`

	// Service names the usual discovery protocol on the group or port. It is
	// empty when there is no well-known protocol. It is a guess from the
	// numbers. Nothing reads the packets.
	Service string `json:"service,omitempty"`

	Bytes    int64     `json:"bytes"`
	Packets  int64     `json:"packets"`
	LastHour time.Time `json:"last_hour"`
}

// DeviceBroadcasts returns what device id sent to everyone since the start of
// the hour of since, most packets first.
func (s *Store) DeviceBroadcasts(ctx context.Context, id int64, since time.Time) ([]*Broadcast, error) {
	rows, err := s.q.DeviceBroadcasts(ctx, models.DeviceBroadcastsParams{
		DeviceID: id,
		Hour:     hourOf(since),
	})
	if err != nil {
		return nil, fmt.Errorf("broadcasts for device %d: %w", id, err)
	}

	out := make([]*Broadcast, 0, len(rows))

	for _, r := range rows {
		dst, err := netip.ParseAddr(r.DstIP)
		if err != nil {
			return nil, fmt.Errorf("broadcast group %q: %w", r.DstIP, err)
		}

		last, err := parseHour("broadcast", r.LastHour)
		if err != nil {
			return nil, err
		}

		b := &Broadcast{
			Dst:      dst,
			Kind:     r.Kind,
			Protocol: uint8(r.Protocol), //nolint:gosec // written from a uint8.
			Port:     uint16(r.Port),    //nolint:gosec // written from a uint16.
			Bytes:    r.Bytes,
			Packets:  r.Packets,
			LastHour: last,
		}
		b.Service = broadcastService(b.Protocol, b.Port, dst)

		out = append(out, b)
	}

	return out, nil
}

// broadcastGroups names the multicast groups of discovery protocols, for a port
// that tells nothing alone.
var broadcastGroups = map[netip.Addr]string{
	netip.MustParseAddr("224.0.0.251"):     "mDNS",
	netip.MustParseAddr("ff02::fb"):        "mDNS",
	netip.MustParseAddr("239.255.255.250"): "SSDP",
	netip.MustParseAddr("ff02::c"):         "SSDP",
	netip.MustParseAddr("224.0.0.252"):     "LLMNR",
	netip.MustParseAddr("ff02::1:3"):       "LLMNR",
}

// broadcastPorts names the discovery protocols by the UDP port on which they
// announce.
var broadcastPorts = map[uint16]string{
	67:    "DHCP",
	68:    "DHCP",
	123:   "NTP",
	137:   "NetBIOS names",
	138:   "NetBIOS datagrams",
	546:   "DHCPv6",
	547:   "DHCPv6",
	1900:  "SSDP",
	1982:  "Yeelight discovery",
	3702:  "WS-Discovery",
	5353:  "mDNS",
	5355:  "LLMNR",
	5678:  "MikroTik neighbour discovery",
	6666:  "Tuya discovery",
	6667:  "Tuya discovery",
	6771:  "BitTorrent local peers",
	9999:  "TP-Link Kasa discovery",
	10001: "Ubiquiti discovery",
	21027: "Syncthing discovery",
	32412: "Plex discovery",
	32414: "Plex discovery",
	38899: "WiZ discovery",
	57621: "Spotify Connect",
}

// broadcastService names the most likely protocol of a broadcast. For UDP, it
// uses the port, then the group. For IGMP and neighbour discovery, it uses the
// protocol.
func broadcastService(protocol uint8, port uint16, dst netip.Addr) string {
	switch protocol {
	case 2:
		return "multicast membership (IGMP)"
	case protoICMPv6:
		return "IPv6 neighbour discovery"
	case protoUDP:
		if name, ok := broadcastPorts[port]; ok {
			return name
		}
	}

	return broadcastGroups[dst]
}
