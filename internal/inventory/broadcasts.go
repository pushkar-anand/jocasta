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

// Where a packet sent to everyone went: the sender's subnet, every host on the
// segment (255.255.255.255), or a multicast group.
const (
	broadcastSubnet    = "subnet"
	broadcastAll       = "all"
	broadcastMulticast = "multicast"
)

// broadcastKey is what one device sent to one group on one port in one hour,
// as a source saw it.
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

// broadcastScope says where a flow went when it went to everyone: to a
// multicast group or to 255.255.255.255. A subnet's own broadcast address
// looks like a host's, and only the recorded networks tell it apart, so Flush
// finds those. A sender with no address of its own, such as a DHCP client
// asking for one, cannot be told apart from any other, and is not kept.
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

// takeSubnetBroadcasts moves what went to a recorded network's broadcast
// address out of pending and into broadcasts. Something sent from one is not a
// host talking, and is dropped.
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

// writeBroadcasts writes one flush's broadcasts inside tx. Only a device's
// are kept: an address no device holds announcing itself says nothing about
// the inventory.
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
