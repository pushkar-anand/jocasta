package inventory

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// Where a packet to everyone went: the subnet of the sender, every host on the
// segment (255.255.255.255), or a multicast group.
const (
	broadcastSubnet    = "subnet"
	broadcastAll       = "all"
	broadcastMulticast = "multicast"
)

// broadcastKey is what one device sent to one group on one port in one hour, as
// a source saw it.
type broadcastKey struct {
	source   string
	kind     dbtype.SourceKind
	hour     time.Time
	src, dst netip.Addr
	scope    string
	protocol uint8
	port     uint16
}

type broadcastTotals struct {
	bytes, packets uint64
}

// broadcastScope tells where a flow to everyone went: to a multicast group, or
// to 255.255.255.255. The broadcast address of a subnet looks like the address
// of a host. Only the recorded networks separate them. So Flush finds those. A
// sender with no address, such as a DHCP client that asks for one, cannot be
// told from any other sender. So the recorder does not keep it.
func broadcastScope(f plugin.Flow) (string, bool) {
	if !f.Src.IsValid() || f.Src.IsUnspecified() || f.Src.IsMulticast() || f.Src == limitedBroadcast {
		return "", false
	}

	switch {
	case f.Dst.IsMulticast():
		return broadcastMulticast, true
	case f.Dst == limitedBroadcast:
		return broadcastAll, true
	}

	return "", false
}

// addBroadcast buffers f as a broadcast. The caller holds r.mu.
func (r *TrafficRecorder) addBroadcast(src plugin.Plugin, f plugin.Flow, scope string) {
	key := broadcastKey{
		source: src.Name(), kind: src.Kind(), hour: f.End.UTC().Truncate(time.Hour),
		src: f.Src, dst: f.Dst, scope: scope, protocol: f.Protocol, port: f.DstPort,
	}

	t, ok := r.broadcasts[key]
	if !ok {
		if len(r.pending)+len(r.broadcasts) >= maxPendingTraffic {
			r.dropped++

			return
		}

		t = &broadcastTotals{}
		r.broadcasts[key] = t
	}

	t.bytes += f.Bytes
	t.packets += f.Packets
}

// takeSubnetBroadcasts moves what went to the broadcast address of a recorded
// network from pending to broadcasts. A packet from such an address is not a
// host that talks. So it drops that packet.
func takeSubnetBroadcasts(
	pending map[trafficKey]*trafficTotals,
	broadcasts map[broadcastKey]*broadcastTotals,
	nets networks,
) {
	for k, t := range pending {
		switch {
		case nets.broadcast(k.src):
			delete(pending, k)
		case nets.broadcast(k.dst):
			delete(pending, k)

			b := broadcastKey{
				source: k.source, kind: k.kind, hour: k.hour, src: k.src, dst: k.dst,
				scope: broadcastSubnet, protocol: k.protocol, port: k.service,
			}

			bt, ok := broadcasts[b]
			if !ok {
				bt = &broadcastTotals{}
				broadcasts[b] = bt
			}

			bt.bytes += t.bytes
			bt.packets += t.packets
		}
	}
}

// writeBroadcasts writes the broadcasts of one flush in tx. It keeps only the
// broadcasts of a device. An address of no device that announces itself tells
// nothing about the inventory.
func writeBroadcasts(
	ctx context.Context,
	q *models.Queries,
	broadcasts map[broadcastKey]*broadcastTotals,
	holders map[netip.Addr]int64,
	sourceID func(name string, kind dbtype.SourceKind) (int64, error),
) error {
	for k, t := range broadcasts {
		device := holders[k.src]
		if device == 0 {
			continue
		}

		srcID, err := sourceID(k.source, k.kind)
		if err != nil {
			return err
		}

		err = q.UpsertBroadcast(ctx, models.UpsertBroadcastParams{
			SourceID: srcID,
			DeviceID: device,
			Hour:     dbtype.NewTime(k.hour),
			DstIP:    dbtype.NewAddr(k.dst),
			Kind:     k.scope,
			Protocol: int64(k.protocol),
			Port:     int64(k.port),
			Bytes:    clampInt64(t.bytes),
			Packets:  clampInt64(t.packets),
		})
		if err != nil {
			return fmt.Errorf("broadcasts for device %d: %w", device, err)
		}
	}

	return nil
}
