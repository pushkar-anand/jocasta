package inventory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/pkg/asn"
)

const (
	// trafficFlushInterval is how often buffered flows are written. A minute
	// keeps the write rate to one transaction a minute however busy the
	// network, and the views ask about hours.
	trafficFlushInterval = time.Minute

	// trafficFlushTimeout bounds the final flush on shutdown, which runs after
	// the context that governed the listener is gone.
	trafficFlushTimeout = 10 * time.Second

	// maxPendingTraffic caps how many distinct conversations one flush
	// interval may buffer. Past it, flows are dropped and counted: a flood of
	// spoofed or scanning traffic should cost only a log line.
	maxPendingTraffic = 200_000

	// Peer names are cached so a busy peer is resolved once; the cache is
	// dropped whole when it grows past its cap, which is
	// simpler than evicting and costs one round of lookups.
	peerNameTTL       = 6 * time.Hour
	maxPeerNames      = 20_000
	nameLookupsAtOnce = 16
)

// TrafficRecorder rolls the flows traffic sources report into hourly totals
// per device and peer.
//
// Flows arrive continuously and are only added up in memory; Run writes them
// once a minute. Which device an address belongs to is settled at that write,
// against the addresses devices hold then, so the totals stay with the device
// after its address moves on.
type TrafficRecorder struct {
	store   *Store
	log     *slog.Logger
	resolve func(context.Context, netip.Addr) string

	mu      sync.Mutex
	pending map[trafficKey]*trafficTotals
	dropped uint64

	// broadcasts are what devices sent to everyone, kept apart from pending
	// since they have no peer.
	broadcasts map[broadcastKey]*broadcastTotals

	// outside are the routers' outside addresses, learned from what they
	// translated flows to, with who last named each and when; routers are the
	// exporters' own addresses.
	outside map[netip.Addr]outsideSeen
	routers map[netip.Addr]bool

	// ports is the ports each device tried on each peer this hour, kept
	// across flushes so an hour's count is of distinct ports. Only Flush
	// touches it.
	ports map[attemptKey]*portSet

	names map[netip.Addr]peerName

	// recent is the last few flushes' conversations, for what is active now.
	recent recentActivity
}

// trafficKey is one conversation direction in one hour, as a source saw it.
type trafficKey struct {
	source   string
	kind     dbtype.SourceKind
	hour     time.Time
	src, dst netip.Addr
	protocol uint8
	service  uint16
}

type trafficTotals struct {
	bytes, packets, connections uint64

	// flows is how many flows were added up; flags every TCP flag they
	// showed. toService is set when they were addressed to the service port,
	// which makes this the direction that started the exchange.
	flows     uint64
	flags     uint8
	toService bool

	// echoRequests and echoReplies count pings and their answers.
	echoRequests, echoReplies uint64
}

type peerName struct {
	name       string
	resolvedAt time.Time
}

// NewTrafficRecorder returns a recorder writing through store. resolve looks
// up a peer's name and may be nil, which records no names.
func NewTrafficRecorder(store *Store, log *slog.Logger, resolve func(context.Context, netip.Addr) string) *TrafficRecorder {
	if log == nil {
		log = slog.Default()
	}

	return &TrafficRecorder{
		store:      store,
		log:        log.With(slog.String("component", "traffic")),
		resolve:    resolve,
		pending:    make(map[trafficKey]*trafficTotals),
		broadcasts: make(map[broadcastKey]*broadcastTotals),
		outside:    make(map[netip.Addr]outsideSeen),
		routers:    make(map[netip.Addr]bool),
		names:      make(map[netip.Addr]peerName),
		ports:      make(map[attemptKey]*portSet),
	}
}

// Add buffers what src reported. It is safe to call from every source's
// listener at once, and never blocks on the database.
func (r *TrafficRecorder) Add(src plugin.Plugin, flows []plugin.Flow) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.learnOutside(src, flows)

	for _, f := range flows {
		f = r.toRouter(f)

		if !conversational(f) {
			if scope, ok := broadcastScope(f); ok {
				r.addBroadcast(src, f, scope)
			}

			continue
		}

		key := trafficKey{
			source:   src.Name(),
			kind:     src.Kind(),
			hour:     f.End.UTC().Truncate(time.Hour),
			src:      f.Src,
			dst:      f.Dst,
			protocol: f.Protocol,
			service:  servicePort(f.Protocol, f.SrcPort, f.DstPort),
		}

		t, ok := r.pending[key]
		if !ok {
			if len(r.pending) >= maxPendingTraffic {
				r.dropped++

				continue
			}

			t = &trafficTotals{}
			r.pending[key] = t
		}

		t.bytes += f.Bytes
		t.packets += f.Packets
		t.flows++
		t.flags |= f.TCPFlags

		// A conversation is usually exported as two flows, one each way.
		// Only the one addressed to the service counts as a connection, so
		// the reply does not count it twice. ICMP has no service port: only a
		// ping starts anything, and every other message (an echo reply, an
		// unreachable, a time exceeded) answers something, so it is never
		// counted as the replier opening a connection.
		switch {
		case f.Protocol == protoICMP || f.Protocol == protoICMPv6:
			if f.ICMPType == icmpEchoRequest || f.ICMPType == icmpv6EchoRequest {
				t.connections++
			}
		case key.service == 0 || f.DstPort == key.service:
			t.connections++
		}

		if key.service != 0 && f.DstPort == key.service {
			t.toService = true
		}

		if f.Protocol == protoICMP || f.Protocol == protoICMPv6 {
			switch f.ICMPType {
			case icmpEchoRequest, icmpv6EchoRequest:
				t.echoRequests++
			case icmpEchoReply, icmpv6EchoReply:
				t.echoReplies++
			}
		}
	}
}

// Run flushes every minute until ctx is done, then once more so nothing
// buffered is lost on a clean shutdown.
func (r *TrafficRecorder) Run(ctx context.Context) error {
	tick := time.NewTicker(trafficFlushInterval)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			if err := r.Flush(ctx); err != nil {
				r.log.ErrorContext(ctx, "flush traffic", slog.Any("error", err))
			}

		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficFlushTimeout)

			err := r.Flush(final)

			cancel()

			if err != nil {
				return fmt.Errorf("final traffic flush: %w", err)
			}

			return nil
		}
	}
}

// Flush writes everything buffered since the last flush in one transaction.
// Run is its only caller in production; the name cache it keeps is not safe
// for two flushes at once.
func (r *TrafficRecorder) Flush(ctx context.Context) error {
	r.mu.Lock()
	pending, broadcasts, dropped := r.pending, r.broadcasts, r.dropped
	r.pending, r.broadcasts, r.dropped = make(map[trafficKey]*trafficTotals), make(map[broadcastKey]*broadcastTotals), 0

	r.forgetOutside(r.store.now())
	routers := maps.Clone(r.routers)
	outside := maps.Clone(r.outside)
	r.mu.Unlock()

	if dropped > 0 {
		r.log.WarnContext(ctx, "dropped flows past the buffer cap",
			slog.Uint64("dropped", dropped), slog.Int("cap", maxPendingTraffic))
	}

	if len(pending) == 0 && len(broadcasts) == 0 {
		return nil
	}

	nets, err := loadNetworks(ctx, r.store.q)
	if err != nil {
		return err
	}

	takeSubnetBroadcasts(pending, broadcasts, nets)

	conversations, attempts := splitAttempts(pending)
	r.samplePorts(attempts, r.store.now())
	r.recent.push(r.store.now(), conversations)

	if err := r.store.recordTraffic(ctx, conversations, attempts, broadcasts, routers, r.peerNames); err != nil {
		return err
	}

	return r.store.recordOutside(ctx, outside)
}

// peerNames returns names for the peers in addrs, resolving the ones the cache
// does not hold concurrently. Called before the write transaction opens, so a
// slow resolver never holds the database.
func (r *TrafficRecorder) peerNames(ctx context.Context, addrs []netip.Addr) map[netip.Addr]string {
	out := make(map[netip.Addr]string, len(addrs))
	if r.resolve == nil {
		return out
	}

	now := r.store.now()

	var todo []netip.Addr

	for _, a := range addrs {
		if n, ok := r.names[a]; ok && now.Sub(n.resolvedAt) < peerNameTTL {
			out[a] = n.name

			continue
		}

		todo = append(todo, a)
	}

	resolved := make([]string, len(todo))

	var g errgroup.Group

	g.SetLimit(nameLookupsAtOnce)

	for i, a := range todo {
		g.Go(func() error {
			resolved[i] = r.resolve(ctx, a)

			return nil
		})
	}

	_ = g.Wait()

	if len(r.names)+len(todo) > maxPeerNames {
		r.names = make(map[netip.Addr]peerName, len(todo))
	}

	for i, a := range todo {
		r.names[a] = peerName{name: resolved[i], resolvedAt: now}
		out[a] = resolved[i]
	}

	return out
}

// recordTraffic writes one flush. Each buffered direction lands on the row of
// whichever end is a known device, and on both rows when both are: the sender
// counts it as sent, the receiver as received. A flow between two addresses
// no device holds says nothing about the inventory and is left out.
func (s *Store) recordTraffic(
	ctx context.Context,
	pending map[trafficKey]*trafficTotals,
	attempts map[attemptKey]*attemptSample,
	broadcasts map[broadcastKey]*broadcastTotals,
	routers map[netip.Addr]bool,
	names func(context.Context, []netip.Addr) map[netip.Addr]string,
) error {
	holders := make(map[netip.Addr]int64)

	holder := func(q *models.Queries, a netip.Addr) (int64, error) {
		if id, ok := holders[a]; ok {
			return id, nil
		}

		d, err := currentHolder(ctx, q, dbtype.NewAddr(a))
		if err != nil {
			return 0, err
		}

		var id int64
		if d != nil {
			id = d.ID
		}

		holders[a] = id

		return id, nil
	}

	// Settle devices first, outside the write, so the peers that need a name
	// are known before any lookup and no lookup runs inside the transaction.
	for k := range pending {
		for _, a := range []netip.Addr{k.src, k.dst} {
			if _, err := holder(s.q, a); err != nil {
				return err
			}
		}
	}

	for k := range attempts {
		for _, a := range []netip.Addr{k.src, k.dst} {
			if _, err := holder(s.q, a); err != nil {
				return err
			}
		}
	}

	for k := range broadcasts {
		if _, err := holder(s.q, k.src); err != nil {
			return err
		}
	}

	var unnamed []netip.Addr

	seen := make(map[netip.Addr]bool)

	for k := range pending {
		// Only a peer across from a device is written, so only that one is
		// worth a lookup.
		for _, pair := range [][2]netip.Addr{{k.src, k.dst}, {k.dst, k.src}} {
			device, peer := pair[0], pair[1]
			if holders[device] != 0 && holders[peer] == 0 && !seen[peer] {
				seen[peer] = true
				unnamed = append(unnamed, peer)
			}
		}
	}

	peerNames := names(ctx, unnamed)

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin traffic: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)
	sources := make(map[string]int64)
	at := s.stamp()

	sourceID := func(name string, kind dbtype.SourceKind) (int64, error) {
		if id, ok := sources[name]; ok {
			return id, nil
		}

		row, err := q.UpsertSource(ctx, models.UpsertSourceParams{Kind: kind, Name: name, CreatedAt: at})
		if err != nil {
			return 0, fmt.Errorf("source %s: %w", name, err)
		}

		sources[name] = row.ID

		return row.ID, nil
	}

	for k, t := range pending {
		srcID, err := sourceID(k.source, k.kind)
		if err != nil {
			return err
		}

		sender, receiver := holders[k.src], holders[k.dst]

		if sender != 0 {
			err := q.UpsertTraffic(ctx, s.trafficRow(srcID, k, sender, k.dst, receiver, peerNames, t, true))
			if err != nil {
				return fmt.Errorf("traffic for device %d: %w", sender, err)
			}
		}

		if receiver != 0 {
			err := q.UpsertTraffic(ctx, s.trafficRow(srcID, k, receiver, k.src, sender, peerNames, t, false))
			if err != nil {
				return fmt.Errorf("traffic for device %d: %w", receiver, err)
			}
		}
	}

	if err := s.writeAttempts(ctx, q, attempts, holders, routers, sourceID); err != nil {
		return err
	}

	if err := writeBroadcasts(ctx, q, broadcasts, holders, sourceID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit traffic: %w", err)
	}

	return nil
}

// conversational reports whether a flow is between two hosts. Broadcast and
// multicast go to everyone listening, so "who did it talk to" has no answer
// and they are kept as broadcasts. An unspecified address is a host that has
// none yet, asking for one.
func conversational(f plugin.Flow) bool {
	for _, a := range []netip.Addr{f.Src, f.Dst} {
		if !a.IsValid() || a.IsUnspecified() || a.IsMulticast() || a == limitedBroadcast {
			return false
		}
	}

	return true
}

var limitedBroadcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})

// trafficRow is one side's view of a buffered direction. sent says whether
// the device is the side that sent it.
func (s *Store) trafficRow(
	sourceID int64,
	k trafficKey,
	device int64,
	peer netip.Addr,
	peerDevice int64,
	names map[netip.Addr]string,
	t *trafficTotals,
	sent bool,
) models.UpsertTrafficParams {
	p := models.UpsertTrafficParams{
		SourceID:     sourceID,
		DeviceID:     device,
		Hour:         dbtype.NewTime(k.hour),
		PeerDeviceID: sql.NullInt64{Int64: peerDevice, Valid: peerDevice != 0},
		PeerIP:       dbtype.NewAddr(peer),
		Protocol:     int64(k.protocol),
		ServicePort:  int64(k.service),
	}

	if peerDevice == 0 {
		p.PeerName = nullString(names[peer])

		if org, ok := asn.Lookup(peer); ok {
			p.PeerASN = sql.NullInt64{Int64: int64(org.ASN), Valid: true}
		}
	}

	bytes, packets := clampInt64(t.bytes), clampInt64(t.packets)

	// The connections this direction counts were started by its sender: the
	// device's own when it sent, the peer's when it received.
	if sent {
		p.BytesOut, p.PacketsOut = bytes, packets
		p.Connections = clampInt64(t.connections)
	} else {
		p.BytesIn, p.PacketsIn = bytes, packets
		p.ConnectionsIn = clampInt64(t.connections)
	}

	return p
}

// servicePort picks the port that names the service in a conversation, from
// whichever side offered it. A protocol without ports has none.
//
// The side a flow came from says nothing on its own, since a reply flows from
// the server, so the choice is made on the ports: a well-known service's port
// below the ephemeral range over any other, a privileged port over an
// unprivileged one, a port below the ephemeral range over one inside it, a
// well-known service's port over one without, and the lower port when nothing
// else decides.
//
// A known port inside the ephemeral range only counts late, because a client
// can draw it as its source port: a laptop that happens to pick 51820 for a
// connection to a game server is not running WireGuard. A server really
// listening there still wins against a client's random port.
func servicePort(protocol uint8, src, dst uint16) uint16 {
	switch protocol {
	case 6, 17, 132: // TCP, UDP, SCTP
	default:
		return 0
	}

	known := func(p uint16) bool { return scanner.ServiceName(p) != "" }
	privileged := func(p uint16) bool { return p < 1024 }
	ephemeral := func(p uint16) bool { return p >= 32768 }
	settled := func(p uint16) bool { return known(p) && !ephemeral(p) }

	for _, prefer := range []func(uint16) bool{settled, privileged, not(ephemeral), known} {
		if prefer(src) != prefer(dst) {
			if prefer(src) {
				return src
			}

			return dst
		}
	}

	return min(src, dst)
}

func not(f func(uint16) bool) func(uint16) bool {
	return func(p uint16) bool { return !f(p) }
}

// clampInt64 stores a counter SQLite can hold. Nothing real reaches the
// limit in an hour; a corrupt export claiming to should not wrap negative.
func clampInt64(v uint64) int64 {
	const maxInt64 = 1<<63 - 1
	if v > maxInt64 {
		return maxInt64
	}

	return int64(v)
}
