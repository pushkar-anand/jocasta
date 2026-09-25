package plugin

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
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

// netFlowPrefix namespaces an instance key so it cannot collide with another
// source kind's.
const netFlowPrefix = "netflow:"

// maxDatagram is the largest UDP payload there is. Exporters keep well under
// the path MTU, so this is headroom.
const maxDatagram = 65535

// ErrNoExporters refuses an empty exporter list: UDP carries
// no authentication and its source address is trivially forged, so an open
// listener would let anyone on the network write whatever traffic they liked
// into the inventory.
var ErrNoExporters = errors.New("plugin: netflow instance lists no exporters")

// NetFlow receives the flows a router exports over NetFlow v5, v9 or IPFIX.
//
// Decoding is goflow2's: its decoders parse the packets and its producer maps
// template fields, uptime-relative timestamps and sampling rates onto one
// record shape. What is left here is the socket, who may send to it, and the
// translation into [Flow].
type NetFlow struct {
	name      string
	listen    string
	exporters map[netip.Addr]struct{}
	logger    *slog.Logger

	// ready, when set, receives the bound address once the socket is open, so
	// a test listening on port zero learns where to send.
	ready func(net.Addr)

	mu     sync.Mutex
	states map[netip.Addr]*exporterState
}

// exporterState is what a v9 or IPFIX exporter has told this listener so far.
//
// Kept per exporter because goflow2 keys templates by version, observation
// domain and template ID, and two routers numbering their templates the same
// way, as two of the same model will, would otherwise decode each other's
// data with the wrong layout.
type exporterState struct {
	templates netflow.NetFlowTemplateSystem
	sampling  protoproducer.SamplingRateSystem
}

// NewNetFlow builds the plugin for one listener. name is the instance key from
// config, listen the UDP address to bind, and exporters the only addresses
// whose datagrams are decoded.
//
// It performs no I/O; the socket opens in Listen.
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

// Name is the source these flows are filed under.
func (n *NetFlow) Name() string { return n.name }

// Kind is ROUTER: whatever exports flows is doing the routing.
func (n *NetFlow) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

// Listen receives datagrams until ctx is done.
//
// A datagram that will not decode is logged and skipped, and the listener
// carries on: one malformed packet, or a v9 data set that arrived before its
// template, says nothing about the next.
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

	// Closing the socket is what unblocks ReadFrom when ctx ends.
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
			// Counted and warned about once: a misdirected exporter sends
			// thousands a minute, and one warning is enough to find it.
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

// decode turns one datagram into flows. received stands in for a flow's end
// time when the exporter sent none.
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
			msgs, err = protoproducer.ProcessMessageIPFIXConfig(&ipfix, st.sampling, nil)
			records, _, _, _ = protoproducer.SplitIPFIXSets(ipfix)
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

			// A record may name the translated address without the port;
			// ICMP has neither port to translate.
			if nat[i].port != 0 {
				f.DstPort = nat[i].port
			}
		}

		flows = append(flows, f)
	}

	return flows, nil
}

// state returns the template and sampling memory for one exporter, creating it
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

// toFlow translates goflow2's record, dropping one without both addresses: a
// template that carries no addresses (an options record, a layer-2-only
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

	end := received
	if m.TimeFlowEndNs > 0 {
		end = time.Unix(0, int64(m.TimeFlowEndNs)) //nolint:gosec // nanoseconds since 1970 fit an int64 until 2262.
	}

	return Flow{
		Src:      src.Unmap(),
		Dst:      dst.Unmap(),
		SrcPort:  uint16(m.SrcPort), //nolint:gosec // a port field is 16 bits on the wire.
		DstPort:  uint16(m.DstPort), //nolint:gosec // as above.
		Protocol: uint8(m.Proto),    //nolint:gosec // the protocol field is 8 bits on the wire.
		TCPFlags: uint8(m.TcpFlags), //nolint:gosec // the low byte holds the flags a flow can show.
		ICMPType: uint8(m.IcmpType), //nolint:gosec // an ICMP type is 8 bits on the wire.
		Bytes:    m.Bytes * scale,
		Packets:  m.Packets * scale,
		End:      end.UTC(),
	}, true
}

// IPFIX information elements for a packet's addresses after the router's NAT.
// v9 numbers them the same.
const (
	iePostNATSrcV4    = 225
	iePostNATDstV4    = 226
	iePostNAPTDstPort = 228
	iePostNATSrcV6    = 281
	iePostNATDstV6    = 282
)

// natDestination is where one record's packets were delivered after NAT, and
// the source they left with, zero when the exporter did not say.
type natDestination struct {
	addr netip.Addr
	port uint16
	src  netip.Addr
}

// postNATDestinations reads each data record's post-NAT destination, in the
// order goflow2's producer turns records into messages: one message per record,
// sets in packet order.
//
// goflow2 maps only the pre-NAT addresses, and on a router doing NAT those are
// the wrong ones for every reply. A reply from the internet is addressed to the
// router's public address and only reaches the device after translation, so
// without this every download would be credited to an address no device holds
// and dropped. The source needs no such fix: an outgoing packet's pre-NAT
// source is already the device.
//
// It returns nothing when the counts disagree, so no record is paired with the
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

// withoutOwnExports drops the flows that carried the exports themselves: an
// exporter sending to this listener sees its own datagrams go by and reports
// them too, so a collector left to count them charts its own feed.
func (n *NetFlow) withoutOwnExports(flows []Flow, port uint16) []Flow {
	return slices.DeleteFunc(flows, func(f Flow) bool {
		_, exporter := n.exporters[f.Src]

		return exporter && f.Protocol == protoUDP && f.DstPort == port
	})
}

// protoUDP is UDP's IANA protocol number, which exports travel over.
const protoUDP = 17

// listenPort is the port a bound socket took, which differs from the
// configured one when that asked for port zero.
func listenPort(a net.Addr) uint16 {
	if ua, ok := a.(*net.UDPAddr); ok {
		return uint16(ua.Port) //nolint:gosec // a bound UDP port fits 16 bits.
	}

	return 0
}

// senderAddr reads the source address off a received datagram, unmapped so an
// IPv4 exporter reaching a dual-stack socket matches its configured address.
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
