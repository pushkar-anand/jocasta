package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/pkg/asn"
)

// IANA protocol numbers and the TCP flags and ICMP types an attempt is told
// apart by.
const (
	protoICMP   = 1
	protoTCP    = 6
	protoUDP    = 17
	protoICMPv6 = 58

	tcpSYN = 0x02
	tcpRST = 0x04
	tcpACK = 0x10

	icmpEchoReply     = 0
	icmpEchoRequest   = 8
	icmpv6EchoRequest = 128
	icmpv6EchoReply   = 129
)

const (
	// attemptPortSample is how many ports, lowest first, an attempt row keeps
	// to say what was tried.
	attemptPortSample = 20

	// maxTrackedPorts caps the ports remembered for one device, peer and
	// hour. Past it, new ports are counted without being remembered, so a
	// full 65535-port sweep costs a bounded amount of memory and its count
	// becomes an estimate.
	maxTrackedPorts = 1024

	// maxPortSets caps how many device, peer and hour combinations are
	// remembered at once; past it the memory is dropped whole, and counts
	// fall back to the larger of the stored one and what came after.
	maxPortSets = 50_000

	// udpStreamPackets is how many packets a one-way UDP exchange needs to be
	// a stream -- logs, video, a game -- rather than a probe nobody answered.
	udpStreamPackets = 4

	// tcpKnockPackets is the most packets, both ways together, a knock can
	// take: a SYN and its RST is two, a handshake closed at once about seven.
	tcpKnockPackets = 10

	// tcpHeaderBytes4 and tcpHeaderBytes6 are the largest a TCP packet
	// carrying no data gets -- a SYN with its options -- over IPv4 and IPv6.
	// A direction averaging more than that carried something.
	tcpHeaderBytes4 = 60
	tcpHeaderBytes6 = 80
)

// attemptKey is one device's attempts at one peer in one hour, as a source
// saw them.
type attemptKey struct {
	source   string
	kind     dbtype.SourceKind
	hour     time.Time
	src, dst netip.Addr
	protocol uint8
}

// attemptSample is what one flush adds for an attemptKey. portCount and ports
// cover the whole hour so far, not just this flush.
type attemptSample struct {
	attempts, answered uint64
	flushPorts         []uint16
	portCount          int
	ports              []uint16

	// lateAnswered counts replies that opened a port knocked on in an earlier
	// flush. They answer attempts already on record rather than this flush's.
	lateAnswered uint64
}

// portSet is the ports tried for one attemptKey this hour.
type portSet struct {
	seen map[uint16]struct{}

	// extra counts ports that arrived after seen was full, so the total is an
	// estimate once it is non-zero.
	extra int
}

func (p *portSet) add(ports []uint16) {
	for _, port := range ports {
		if _, ok := p.seen[port]; ok {
			continue
		}

		if len(p.seen) >= maxTrackedPorts {
			p.extra++

			continue
		}

		p.seen[port] = struct{}{}
	}
}

func (p *portSet) count() int { return len(p.seen) + p.extra }

func (p *portSet) lowest() []uint16 {
	out := make([]uint16, 0, len(p.seen))
	for port := range p.seen {
		out = append(out, port)
	}

	slices.Sort(out)

	return out[:min(len(out), attemptPortSample)]
}

// pairID is one exchange regardless of direction: the directions buffered
// for it are judged together, since whether it was answered is in the one
// that came back.
type pairID struct {
	source   string
	hour     time.Time
	protocol uint8
	service  uint16
	a, b     netip.Addr
}

func pairOf(k trafficKey) pairID {
	a, b := k.src, k.dst
	if b.Less(a) {
		a, b = b, a
	}

	return pairID{source: k.source, hour: k.hour, protocol: k.protocol, service: k.service, a: a, b: b}
}

// splitAttempts sorts one flush into conversations -- data went somewhere --
// and attempts that never carried any: a port knocked on, a SYN nobody
// answered, a ping, a one-off UDP datagram. The first become traffic rows; the
// second are counted per peer, so a scan that knocks on a thousand ports is
// one row a peer rather than a thousand.
// oneWayByDesign reports whether UDP on service is one-way in what a router
// exports: DHCP. A client's request goes to the router, and the answer is a
// broadcast or comes from the router itself, neither of which the router's
// export carries -- so every renewal would look like a try nobody answered.
func oneWayByDesign(service uint16) bool {
	switch service {
	case 67, 68, 546, 547: // DHCP server and client, DHCPv6 client and server.
		return true
	}

	return false
}

func splitAttempts(pending map[trafficKey]*trafficTotals) (map[trafficKey]*trafficTotals, map[attemptKey]*attemptSample) {
	pairs := make(map[pairID][]trafficKey)

	for k := range pending {
		p := pairOf(k)
		pairs[p] = append(pairs[p], k)
	}

	conversations := make(map[trafficKey]*trafficTotals, len(pending))
	attempts := make(map[attemptKey]*attemptSample)

	record := func(from trafficKey, n, answered uint64, port uint16) {
		k := attemptKey{source: from.source, kind: from.kind, hour: from.hour, src: from.src, dst: from.dst, protocol: from.protocol}

		a, ok := attempts[k]
		if !ok {
			a = &attemptSample{}
			attempts[k] = a
		}

		a.attempts += n
		a.answered += min(answered, n)

		if port != 0 {
			a.flushPorts = append(a.flushPorts, port)
		}
	}

	// late credits a reply to the attempt it answers, the reverse direction.
	late := func(reply trafficKey, n uint64) {
		k := attemptKey{source: reply.source, kind: reply.kind, hour: reply.hour, src: reply.dst, dst: reply.src, protocol: reply.protocol}

		a, ok := attempts[k]
		if !ok {
			a = &attemptSample{}
			attempts[k] = a
		}

		a.lateAnswered += n
	}

	keep := func(keys []trafficKey) {
		for _, k := range keys {
			conversations[k] = pending[k]
		}
	}

	for _, keys := range pairs {
		reverse := func(k trafficKey) *trafficTotals {
			for _, o := range keys {
				if o.src == k.dst && o.dst == k.src {
					return pending[o]
				}
			}

			return nil
		}

		switch keys[0].protocol {
		case protoTCP:
			if reply, ok := lateReply(keys, pending); ok {
				// The knock it answers was counted in an earlier flush; a
				// refusal adds nothing to it, an open port answers it.
				if t := pending[reply]; t.flags&tcpSYN != 0 {
					late(reply, t.flows)
				}

				continue
			}

			init, ok := initiator(keys, pending)
			if !ok || !tcpKnock(keys, pending, init) {
				keep(keys)

				continue
			}

			t := pending[init]
			answered := uint64(0)

			if r := reverse(init); r != nil && (r.flags == 0 || r.flags&(tcpSYN|tcpACK) == tcpSYN|tcpACK) {
				answered = r.flows
			}

			record(init, t.connections, answered, init.service)

		case protoUDP:
			if len(keys) > 1 {
				keep(keys)

				continue
			}

			t := pending[keys[0]]
			if !t.toService || t.packets > udpStreamPackets || oneWayByDesign(keys[0].service) {
				keep(keys)

				continue
			}

			record(keys[0], t.connections, 0, keys[0].service)

		case protoICMP, protoICMPv6:
			for _, k := range keys {
				t := pending[k]

				switch {
				case t.echoRequests > 0:
					answered := uint64(0)
					if r := reverse(k); r != nil {
						answered = r.echoReplies
					}

					record(k, t.echoRequests, answered, 0)
				case t.echoReplies > 0:
					// The answer to a ping, counted with it when the ping is
					// in this flush, and credited to it on record when the
					// ping came a flush earlier.
					if r := reverse(k); r == nil || r.echoRequests == 0 {
						late(k, t.echoReplies)
					}
				default:
					// Unreachables and the like: not an attempt by either side.
					conversations[k] = t
				}
			}

		default:
			keep(keys)
		}
	}

	return conversations, attempts
}

// initiator is the direction of a TCP exchange addressed to its service port.
func initiator(keys []trafficKey, pending map[trafficKey]*trafficTotals) (trafficKey, bool) {
	for _, k := range keys {
		if pending[k].toService {
			return k, true
		}
	}

	return trafficKey{}, false
}

// lateReply reports whether a TCP exchange is only the answer to a knock:
// one direction, from the service port, header-sized, a handful of packets,
// opening with SYN+ACK (the port was open) or RST (it was not). The router
// exports each direction as its own flow, and the two need not land in the
// same flush; the knock was then counted on its own, and its answer is not a
// conversation.
//
// An exporter that sends no flags leaves the answer unreadable, so the
// exchange is kept as it was.
func lateReply(keys []trafficKey, pending map[trafficKey]*trafficTotals) (trafficKey, bool) {
	if len(keys) != 1 {
		return trafficKey{}, false
	}

	k := keys[0]
	t := pending[k]

	header := uint64(tcpHeaderBytes4)
	if k.src.Is6() {
		header = tcpHeaderBytes6
	}

	switch {
	case t.toService, t.packets > tcpKnockPackets, t.bytes > t.packets*header:
		return trafficKey{}, false
	case t.flags&(tcpSYN|tcpACK) == tcpSYN|tcpACK, t.flags&tcpRST != 0 && t.flags&tcpSYN == 0:
		return k, true
	}

	return trafficKey{}, false
}

// tcpKnock reports whether a TCP exchange opened a connection and sent no
// data through it: a port knocked on and refused, or opened and shut at once.
//
// Payload is read off the sizes, not the PSH flag, because exporters do not
// agree on what the flags field holds: some OR every packet's flags, a
// MikroTik reports only the first packet's. The first packet is still what
// tells a new connection (SYN) from a long one that only traded keepalives
// this minute, which is a conversation.
func tcpKnock(keys []trafficKey, pending map[trafficKey]*trafficTotals, init trafficKey) bool {
	var packets uint64

	for _, k := range keys {
		t := pending[k]
		packets += t.packets

		header := uint64(tcpHeaderBytes4)
		if k.src.Is6() {
			header = tcpHeaderBytes6
		}

		if t.bytes > t.packets*header {
			return false
		}
	}

	if packets > tcpKnockPackets {
		return false
	}

	flags := pending[init].flags

	return flags == 0 || flags&tcpSYN != 0
}

// samplePorts folds each attempt's ports from this flush into what the
// recorder remembers for the hour, and fills in the hour's count and sample.
// Hours that ended more than an hour ago are forgotten: exports arrive late,
// but not that late.
func (r *TrafficRecorder) samplePorts(attempts map[attemptKey]*attemptSample, now time.Time) {
	cutoff := now.UTC().Truncate(time.Hour).Add(-time.Hour)

	for k := range r.ports {
		if k.hour.Before(cutoff) {
			delete(r.ports, k)
		}
	}

	if len(r.ports)+len(attempts) > maxPortSets {
		r.ports = make(map[attemptKey]*portSet, len(attempts))
	}

	for k, a := range attempts {
		if len(a.flushPorts) == 0 {
			continue
		}

		set, ok := r.ports[k]
		if !ok {
			set = &portSet{seen: make(map[uint16]struct{})}
			r.ports[k] = set
		}

		set.add(a.flushPorts)
		a.portCount = set.count()
		a.ports = set.lowest()
	}
}

// writeAttempts writes one flush's attempts inside tx. Only attempts a
// device made are kept: what an address no device holds tried says nothing
// about the inventory.
func (s *Store) writeAttempts(
	ctx context.Context,
	q *models.Queries,
	attempts map[attemptKey]*attemptSample,
	holders map[netip.Addr]int64,
	sourceID func(name string, kind dbtype.SourceKind) (int64, error),
) error {
	for k, a := range attempts {
		device := holders[k.src]
		if device == 0 {
			continue
		}

		srcID, err := sourceID(k.source, k.kind)
		if err != nil {
			return err
		}

		if a.attempts > 0 {
			if err := s.upsertAttempts(ctx, q, k, a, device, srcID, holders); err != nil {
				return err
			}
		}

		// After this flush's own attempts, so a reply to a knock made in
		// this flush and an earlier one both find a row. A reply whose knock
		// fell in the previous hour finds none and is lost.
		if a.lateAnswered > 0 {
			err := q.AnswerAttempts(ctx, models.AnswerAttemptsParams{
				Late: clampInt64(a.lateAnswered), DeviceID: device, Hour: dbtype.NewTime(k.hour),
				SourceID: srcID, PeerIP: dbtype.NewAddr(k.dst), Protocol: int64(k.protocol),
			})
			if err != nil {
				return fmt.Errorf("late answers for device %d: %w", device, err)
			}
		}
	}

	return nil
}

// upsertAttempts adds one flush's attempts at one peer to the hour's row.
func (s *Store) upsertAttempts(
	ctx context.Context,
	q *models.Queries,
	k attemptKey,
	a *attemptSample,
	device, srcID int64,
	holders map[netip.Addr]int64,
) error {
	p := models.UpsertAttemptsParams{
		SourceID:  srcID,
		DeviceID:  device,
		Hour:      dbtype.NewTime(k.hour),
		PeerIP:    dbtype.NewAddr(k.dst),
		Protocol:  int64(k.protocol),
		Attempts:  clampInt64(a.attempts),
		Answered:  clampInt64(a.answered),
		PortCount: int64(a.portCount),
	}

	if peer := holders[k.dst]; peer != 0 {
		p.PeerDeviceID = sql.NullInt64{Int64: peer, Valid: true}
	} else if org, ok := asn.Lookup(k.dst); ok {
		p.PeerASN = sql.NullInt64{Int64: int64(org.ASN), Valid: true}
	}

	ports := a.ports

	// Merge with what an earlier flush -- or an earlier run of this
	// process -- stored for the hour.
	prev, err := q.AttemptPorts(ctx, models.AttemptPortsParams{
		DeviceID: device, Hour: p.Hour, SourceID: srcID, PeerIP: p.PeerIP, Protocol: p.Protocol,
	})

	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("attempt ports for device %d: %w", device, err)
	default:
		ports = mergePorts(ports, parsePortList(prev.Ports))
		p.PortCount = max(p.PortCount, prev.PortCount, int64(len(ports)))
	}

	p.Ports = formatPorts(ports)

	if err := q.UpsertAttempts(ctx, p); err != nil {
		return fmt.Errorf("attempts for device %d: %w", device, err)
	}

	return nil
}

// mergePorts is the lowest attemptPortSample ports of a and b together.
func mergePorts(a, b []uint16) []uint16 {
	out := slices.Concat(a, b)
	slices.Sort(out)
	out = slices.Compact(out)

	return out[:min(len(out), attemptPortSample)]
}

func formatPorts(ports []uint16) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = strconv.Itoa(int(p))
	}

	return strings.Join(parts, ",")
}

// parsePortList reads a comma-separated port list, skipping anything that is
// not a port.
func parsePortList(s string) []uint16 {
	var out []uint16

	for part := range strings.SplitSeq(s, ",") {
		if n, err := strconv.ParseUint(strings.TrimSpace(part), 10, 16); err == nil && n != 0 {
			out = append(out, uint16(n))
		}
	}

	return out
}
