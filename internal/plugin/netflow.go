package plugin

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/bits"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/netsampler/goflow2/v2/decoders/netflow"
	"github.com/netsampler/goflow2/v2/decoders/netflowlegacy"
	"github.com/netsampler/goflow2/v2/producer"
	protoproducer "github.com/netsampler/goflow2/v2/producer/proto"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// netFlowPrefix puts an instance key in a namespace. So it cannot collide with
// the key of a different source kind.
const netFlowPrefix = "netflow:"

// maxDatagram is the largest possible UDP payload. Exporters stay well below
// the path MTU. So this is more than enough.
const maxDatagram = 65535

// ErrNoExporters refuses an empty exporter list. UDP has no authentication, and
// its source address is easy to forge. So with an open listener, anyone on
// the network could write any traffic into the inventory.
var ErrNoExporters = errors.New("plugin: netflow instance lists no exporters")

// NetFlow receives the flows that a router exports over NetFlow v5, v9 or
// IPFIX.
//
// goflow2 does the decoding. Its decoders parse the packets. Its producer maps
// template fields, timestamps relative to uptime, and sampling rates to one
// record shape. This type does the rest: the socket, who can send to it, and
// the translation into [Flow].
type NetFlow struct {
	name      string
	listen    string
	exporters map[netip.Addr]struct{}
	logger    *slog.Logger

	// ready, when set, receives the bound address when the socket is open. So
	// a test that listens on port zero learns where to send.
	ready func(net.Addr)

	mu     sync.Mutex
	states map[netip.Addr]*exporterState
}

// exporterState is what a v9 or IPFIX exporter told this listener until now.
//
// The listener keeps one for each exporter. goflow2 keys templates by version,
// observation domain and template ID. Two routers of the same model give their
// templates the same numbers. Without this, each would decode the data of the
// other with the wrong layout.
type exporterState struct {
	templates netflow.NetFlowTemplateSystem
	sampling  protoproducer.SamplingRateSystem
}

// NewNetFlow builds the plugin for one listener. name is the instance key from
// config. listen is the UDP address to bind. exporters are the only addresses
// whose datagrams the listener decodes. A device on the same network can forge
// the address of an exporter. So forged flows stay out only with a listen
// address that only the router reaches, or with a firewall rule.
//
// It does no I/O. The socket opens in Listen.
func NewNetFlow(name, listen string, exporters []string, log *slog.Logger) (*NetFlow, error) {
	if name == "" {
		return nil, fmt.Errorf("plugin: netflow instance has no name")
	}

	if len(exporters) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoExporters, name)
	}

	allowed := make(map[netip.Addr]struct{}, len(exporters))

	for _, e := range exporters {
		addr, err := netip.ParseAddr(e)
		if err != nil {
			return nil, fmt.Errorf("plugin: netflow %q exporter %q: %w", name, e, err)
		}

		allowed[addr.Unmap()] = struct{}{}
	}

	if log == nil {
		log = slog.Default()
	}

	return &NetFlow{
		name:      netFlowPrefix + name,
		listen:    listen,
		exporters: allowed,
		logger:    log.With(slog.String("plugin", netFlowPrefix+name)),
		states:    make(map[netip.Addr]*exporterState),
	}, nil
}

// Name is the source of these flows.
func (n *NetFlow) Name() string { return n.name }

// Kind is ROUTER. The device that exports flows is the device that routes.
func (n *NetFlow) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

// Listen receives datagrams until ctx is done.
//
// The listener logs and skips a datagram that does not decode, and continues.
// One malformed packet, or a v9 data set that arrived before its template,
// tells nothing about the next.
func (n *NetFlow) Listen(ctx context.Context, emit func(context.Context, []Flow)) error {
	var lc net.ListenConfig

	conn, err := lc.ListenPacket(ctx, "udp", n.listen)
	if err != nil {
		return fmt.Errorf("plugin: netflow %s listen %s: %w", n.name, n.listen, err)
	}

	n.logger.InfoContext(ctx, "listening for flow exports", slog.String("addr", conn.LocalAddr().String()))

	if n.ready != nil {
		n.ready(conn.LocalAddr())
	}

	// The close of the socket unblocks ReadFrom when ctx ends.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	var refused uint64

	port := listenPort(conn.LocalAddr())
	buf := make([]byte, maxDatagram)

	for {
		size, from, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				if refused > 0 {
					n.logger.InfoContext(ctx, "dropped datagrams from unlisted senders", slog.Uint64("count", refused))
				}

				return nil
			}

			return fmt.Errorf("plugin: netflow %s read: %w", n.name, err)
		}

		sender := senderAddr(from)
		if _, ok := n.exporters[sender]; !ok {
			// Count these, and warn once. An exporter with the wrong target
			// sends thousands each minute. One warning is enough to find it.
			if refused == 0 {
				n.logger.WarnContext(ctx, "dropping datagrams from a sender not listed in exporters",
					slog.String("sender", sender.String()))
			}

			refused++

			continue
		}

		flows, err := n.decode(sender, buf[:size], time.Now())
		if err != nil {
			n.logger.DebugContext(ctx, "skipping undecodable datagram",
				slog.String("sender", sender.String()), slog.Any("error", err))

			continue
		}

		flows = n.withoutOwnExports(flows, port)

		if len(flows) > 0 {
			emit(ctx, flows)
		}
	}
}

// decode turns one datagram into flows. received replaces the end time of a
// flow when the exporter sent none, or one more than [maxFlowEndSkew] from
// it.
func (n *NetFlow) decode(sender netip.Addr, payload []byte, received time.Time) ([]Flow, error) {
	if len(payload) < 2 {
		return nil, errors.New("datagram too short for a version")
	}

	var (
		msgs    []producer.ProducerMessage
		records []netflow.DataFlowSet
		err     error
	)

	switch version := binary.BigEndian.Uint16(payload); version {
	case 5:
		var pkt netflowlegacy.PacketNetFlowV5
		if err := netflowlegacy.DecodeMessageVersion(bytes.NewBuffer(payload), &pkt); err != nil {
			return nil, err
		}

		msgs, err = protoproducer.ProcessMessageNetFlowLegacy(&pkt)

	case 9, 10:
		st := n.state(sender)

		var (
			v9    netflow.NFv9Packet
			ipfix netflow.IPFIXPacket
		)

		if err := netflow.DecodeMessageVersion(bytes.NewBuffer(payload), st.templates, &v9, &ipfix); err != nil {
			return nil, err
		}

		if version == 9 {
			msgs, err = protoproducer.ProcessMessageNetFlowV9Config(&v9, st.sampling, nil)
			records, _, _, _ = protoproducer.SplitNetFlowSets(v9)
		} else {
			var options []netflow.OptionsDataFlowSet

			records, _, _, options = protoproducer.SplitIPFIXSets(ipfix)
			msgs, err = protoproducer.ProcessMessageIPFIXConfig(&ipfix, st.sampling, nil)

			// goflow2 takes samplingPacketInterval as the rate. This is right
			// only when nothing is skipped. Some exporters tell how many they
			// skip, as softflowd does. For them, this code calculates the rate.
			// It replaces the rate of goflow2 for this packet and the next
			// packets.
			if rate, ok := packetSpaceRate(options); ok && err == nil {
				st.sampling.AddSamplingRate(10, ipfix.ObservationDomainId, rate)

				for _, m := range msgs {
					if pm, ok := m.(*protoproducer.ProtoProducerMessage); ok {
						pm.SamplingRate = uint64(rate)
					}
				}
			}
		}

	default:
		return nil, fmt.Errorf("unknown export version %d", version)
	}

	if err != nil {
		return nil, err
	}

	nat := postNATDestinations(records, len(msgs))
	flows := make([]Flow, 0, len(msgs))

	for i, m := range msgs {
		pm, ok := m.(*protoproducer.ProtoProducerMessage)
		if !ok {
			continue
		}

		f, ok := toFlow(pm, received)
		if !ok {
			continue
		}

		f.Exporter = sender

		if i < len(nat) && nat[i].src.IsValid() && nat[i].src != f.Src {
			f.NATSrc = nat[i].src
		}

		if i < len(nat) && nat[i].addr.IsValid() {
			f.Dst = nat[i].addr

			// A record can name the translated address without the port. ICMP
			// has no port to translate.
			if nat[i].port != 0 {
				f.DstPort = nat[i].port
			}
		}

		flows = append(flows, f)
	}

	return flows, nil
}

// IPFIX information elements that describe count-based sampling (RFC 5477).
// Select samplingPacketInterval packets, then skip samplingPacketSpace.
const (
	ieSamplingPacketInterval = 305
	ieSamplingPacketSpace    = 306
)

// packetSpaceRate returns the sampling rate that an IPFIX options record gives
// as a packet interval and space. An example is 1 and 99 from softflowd, for
// one packet in 100. It returns false when no record gives both.
func packetSpaceRate(options []netflow.OptionsDataFlowSet) (uint32, bool) {
	for _, set := range options {
		for _, rec := range set.Records {
			var interval, space uint32

			hasInterval, err := protoproducer.NetFlowPopulate(rec.OptionsValues, ieSamplingPacketInterval, &interval)
			if err != nil || !hasInterval || interval == 0 {
				continue
			}

			hasSpace, err := protoproducer.NetFlowPopulate(rec.OptionsValues, ieSamplingPacketSpace, &space)
			if err != nil || !hasSpace {
				continue
			}

			rate := (uint64(interval) + uint64(space)) / uint64(interval)
			if rate > math.MaxUint32 {
				continue
			}

			return uint32(rate), true
		}
	}

	return 0, false
}

// state returns the template and sampling memory of one exporter. It creates it
// on first contact.
func (n *NetFlow) state(sender netip.Addr) *exporterState {
	n.mu.Lock()
	defer n.mu.Unlock()

	st, ok := n.states[sender]
	if !ok {
		st = &exporterState{
			templates: netflow.CreateTemplateSystem(),
			sampling:  protoproducer.CreateSamplingSystem(),
		}
		n.states[sender] = st
	}

	return st
}

// toFlow translates the record of goflow2. It drops a record without both
// addresses. A template with no addresses (an options record, a layer-2-only
// export) describes no conversation between hosts.
func toFlow(m *protoproducer.ProtoProducerMessage, received time.Time) (Flow, bool) {
	src, ok := netip.AddrFromSlice(m.SrcAddr)
	if !ok {
		return Flow{}, false
	}

	dst, ok := netip.AddrFromSlice(m.DstAddr)
	if !ok {
		return Flow{}, false
	}

	// Sampling rates of 0 and 1 both mean every packet was counted.
	scale := max(m.SamplingRate, 1)

	// A router with a wrong clock would put the flow in an hour long in the
	// past or in the future. So the arrival replaces an end that far away. It
	// also replaces a missing end.
	end := time.Unix(0, int64(m.TimeFlowEndNs)) //nolint:gosec // nanoseconds since 1970 fit an int64 until 2262.
	if m.TimeFlowEndNs == 0 || end.Sub(received).Abs() > maxFlowEndSkew {
		end = received
	}

	return Flow{
		Src:      src.Unmap(),
		Dst:      dst.Unmap(),
		SrcPort:  uint16(m.SrcPort), //nolint:gosec // a port field is 16 bits on the wire.
		DstPort:  uint16(m.DstPort), //nolint:gosec // as above.
		Protocol: uint8(m.Proto),    //nolint:gosec // the protocol field is 8 bits on the wire.
		TCPFlags: uint8(m.TcpFlags), //nolint:gosec // the low byte holds the flags a flow can show.
		ICMPType: uint8(m.IcmpType), //nolint:gosec // an ICMP type is 8 bits on the wire.
		Bytes:    scaled(m.Bytes, scale),
		Packets:  scaled(m.Packets, scale),
		End:      end.UTC(),
	}, true
}

// maxFlowEndSkew is how far the end of a flow can be from the arrival of its
// export. A router exports a flow when it ends, or at its active timeout. So a
// true end is minutes before the arrival.
const maxFlowEndSkew = time.Hour

// scaled returns n times rate, or the largest uint64 when that does not fit. A
// forged or corrupt record can have any count and any rate.
func scaled(n, rate uint64) uint64 {
	hi, lo := bits.Mul64(n, rate)
	if hi != 0 {
		return math.MaxUint64
	}

	return lo
}

// IPFIX information elements for the addresses of a packet after the NAT of
// the router. v9 gives them the same numbers.
const (
	iePostNATSrcV4    = 225
	iePostNATDstV4    = 226
	iePostNAPTDstPort = 228
	iePostNATSrcV6    = 281
	iePostNATDstV6    = 282
)

// natDestination is where the packets of one record arrived after NAT, and the
// source with which they left. It is zero when the exporter did not tell.
type natDestination struct {
	addr netip.Addr
	port uint16
	src  netip.Addr
}

// postNATDestinations reads the post-NAT destination of each data record. It
// uses the order in which the producer of goflow2 turns records into messages:
// one message for each record, with sets in packet order.
//
// goflow2 maps only the pre-NAT addresses. On a router that does NAT, those are
// the wrong addresses for every reply. A reply from the internet goes to the
// public address of the router. It reaches the device only after translation.
// So without this, every download would go to an address that no device has,
// and would be dropped. The source needs no such fix. The pre-NAT source of
// an outgoing packet is already the device.
//
// It returns nothing when the counts are different. So no record goes with the
// wrong message.
func postNATDestinations(sets []netflow.DataFlowSet, messages int) []natDestination {
	var out []natDestination

	for _, s := range sets {
		for _, r := range s.Records {
			var d natDestination

			for _, v := range r.Values {
				b, ok := v.Value.([]byte)
				if !ok || v.PenProvided {
					continue
				}

				switch {
				case (v.Type == iePostNATDstV4 && len(b) == 4) || (v.Type == iePostNATDstV6 && len(b) == 16):
					if a, ok := netip.AddrFromSlice(b); ok && !a.IsUnspecified() {
						d.addr = a.Unmap()
					}
				case v.Type == iePostNAPTDstPort && len(b) == 2:
					d.port = binary.BigEndian.Uint16(b)
				case (v.Type == iePostNATSrcV4 && len(b) == 4) || (v.Type == iePostNATSrcV6 && len(b) == 16):
					if a, ok := netip.AddrFromSlice(b); ok && !a.IsUnspecified() {
						d.src = a.Unmap()
					}
				}
			}

			out = append(out, d)
		}
	}

	if len(out) != messages {
		return nil
	}

	return out
}

// withoutOwnExports drops the flows that carried the exports themselves. An
// exporter that sends to this listener sees its own datagrams, and also
// reports them. So a collector that counts them would chart its own feed.
func (n *NetFlow) withoutOwnExports(flows []Flow, port uint16) []Flow {
	return slices.DeleteFunc(flows, func(f Flow) bool {
		_, exporter := n.exporters[f.Src]

		return exporter && f.Protocol == protoUDP && f.DstPort == port
	})
}

// protoUDP is the IANA protocol number of UDP. Exports go over UDP.
const protoUDP = 17

// listenPort is the port of a bound socket. It is different from the
// configured port when the configured port is zero.
func listenPort(a net.Addr) uint16 {
	if ua, ok := a.(*net.UDPAddr); ok {
		return uint16(ua.Port) //nolint:gosec // a bound UDP port fits 16 bits.
	}

	return 0
}

// senderAddr reads the source address from a received datagram, unmapped. So
// an IPv4 exporter on a dual-stack socket matches its configured address.
func senderAddr(from net.Addr) netip.Addr {
	if ua, ok := from.(*net.UDPAddr); ok {
		return ua.AddrPort().Addr().Unmap()
	}

	ap, err := netip.ParseAddrPort(from.String())
	if err != nil {
		return netip.Addr{}
	}

	return ap.Addr().Unmap()
}

var _ TrafficReporter = (*NetFlow)(nil)
