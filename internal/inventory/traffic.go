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
	// trafficFlushInterval is how often the recorder writes buffered flows. A
	// minute keeps the write rate at one transaction each minute, for any
	// network. The views ask about hours.
	trafficFlushInterval = time.Minute

	// trafficFlushTimeout limits the final flush on shutdown. That flush runs
	// after the context of the listener is gone.
	trafficFlushTimeout = 10 * time.Second

	// maxPendingTraffic limits how many different conversations one flush
	// interval can buffer. After it, the recorder drops and counts flows. A
	// flood of forged or scanning traffic must cost only a log line.
	maxPendingTraffic = 200_000

	// The recorder caches peer names. So it resolves a busy peer once. When
	// the cache grows past its limit, the recorder drops all of it. This is
	// simpler than eviction, and costs one round of lookups.
	peerNameTTL       = 6 * time.Hour
	maxPeerNames      = 20_000
	nameLookupsAtOnce = 16
)

// TrafficRecorder adds the flows from traffic sources into hourly totals for
// each device and peer.
//
// Flows arrive all the time. The recorder adds them up in memory. Run writes
// them once a minute. That write decides the device of an address, against
// the addresses of devices at that time. So the totals stay with the device
// after its address changes.
type TrafficRecorder struct {
	store   *Store
	log     *slog.Logger
	resolve func(context.Context, netip.Addr) string

	mu      sync.Mutex
	pending map[trafficKey]*trafficTotals
	dropped uint64

	// broadcasts are what devices sent to everyone. They stay separate from
	// pending, because they have no peer.
	broadcasts map[broadcastKey]*broadcastTotals

	// outside are the outside addresses of the routers. The recorder learns
	// them from the translated flows. It keeps who last named each, and when.
	// routers are the own addresses of the exporters.
	outside map[netip.Addr]outsideSeen
	routers map[netip.Addr]bool

	// ports is the ports that each device tried on each peer this hour. It
	// stays across flushes. So the count of an hour is of different ports.
	// Only Flush uses it.
	ports map[attemptKey]*portSet

	names map[netip.Addr]peerName

	// recent is the conversations of the last few flushes, for what is active
	// now.
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

	// flows is how many flows the recorder added up. flags is every TCP flag
	// that they showed. toService is set when they went to the service port.
	// This makes this the direction that started the exchange.
	flows     uint64
	flags     uint8
	toService bool

	// mirrored is how many of connections came from flows with the same port
	// on both ends. Their reply also goes to the service.
	mirrored uint64

	// echoRequests and echoReplies count pings and their answers.
	echoRequests, echoReplies uint64
}

type peerName struct {
	name       string
	resolvedAt time.Time
}

// NewTrafficRecorder returns a recorder that writes through store. resolve
// looks up the name of a peer. It can be nil. Then the recorder records no
// names.
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

// Add buffers what src reported. The listeners of all sources can call it at
// once. It never blocks on the database.
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

		// An exporter usually exports a conversation as two flows, one in
		// each direction. Only the flow to the service counts as a
		// connection. So the reply does not count it twice. ICMP has no
		// service port. Only a ping starts something. Every other message (an
		// echo reply, an unreachable, a time exceeded) answers something. So
		// it never counts as a connection opened by the replier.
		switch {
		case f.Protocol == protoICMP || f.Protocol == protoICMPv6:
			if f.ICMPType == icmpEchoRequest || f.ICMPType == icmpv6EchoRequest {
				t.connections++
			}
		case key.service == 0 || f.DstPort == key.service:
			t.connections++

			// An NTP client sends from 123 to 123. So the reply of its server
			// also counts here. trafficRow decides which side started it.
			if key.service != 0 && f.SrcPort == f.DstPort {
				t.mirrored++
			}
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

// Run flushes every minute until ctx is done. Then it flushes once more. So a
// clean shutdown loses no buffered data.
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

// Flush writes everything buffered since the last flush, in one transaction.
// In production, only Run calls it. Its name cache is not safe for two flushes
// at once.
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

// peerNames returns names for the peers in addrs. It resolves the peers that
// are not in the cache at the same time. It runs before the write transaction
// opens. So a slow resolver never holds the database.
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

// recordTraffic writes one flush. Each buffered direction goes on the row of
// the end that is a known device. If both ends are, it goes on both rows. The
// sender counts it as sent, and the receiver as received. A flow between two
// addresses of no device tells nothing about the inventory. So it is left
// out.
func (s *Store) recordTraffic(
	ctx context.Context,
	pending map[trafficKey]*trafficTotals,
	attempts map[attemptKey]*attemptSample,
	broadcasts map[broadcastKey]*broadcastTotals,
	routers map[netip.Addr]bool,
	names func(context.Context, []netip.Addr) map[netip.Addr]string,
) error {
	// One read settles every device, outside the write. So the peers that need
	// a name are known before a lookup. No lookup runs in the transaction. An
	// address that is not in the map belongs to no device.
	holders, err := s.currentHolders(ctx)
	if err != nil {
		return err
	}

	var unnamed []netip.Addr

	seen := make(map[netip.Addr]bool)

	for k := range pending {
		// Only a peer of a device is written. So only that peer is worth a
		// lookup.
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
// multicast go to every listener. So "who did it talk to" has no answer, and
// the recorder keeps them as broadcasts. An unspecified address is a host that
// has no address yet, and asks for one.
func conversational(f plugin.Flow) bool {
	for _, a := range []netip.Addr{f.Src, f.Dst} {
		if !a.IsValid() || a.IsUnspecified() || a.IsMulticast() || a == limitedBroadcast {
			return false
		}
	}

	return true
}

var limitedBroadcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})

// trafficRow is the view of one side of a buffered direction. sent tells
// whether the device is the side that sent it.
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

	// The sender of this direction started its connections. When the device
	// sent, they are its own. When it received, they are the connections of
	// the peer.
	if sent {
		p.BytesOut, p.PacketsOut = bytes, packets
		p.Connections = clampInt64(t.connections)
	} else {
		p.BytesIn, p.PacketsIn = bytes, packets
		p.ConnectionsIn = clampInt64(t.connections)

		// With the same port on both ends, the ports cannot tell the opener
		// from the replier. The recorder takes an internet peer as the
		// replier. A device here is much more likely the client of an NTP or
		// IKE server than a server itself.
		if p.PeerASN.Valid {
			p.ConnectionsIn = clampInt64(t.connections - t.mirrored)
		}
	}

	return p
}

// servicePort picks the port that names the service in a conversation, from
// the side that offered it. A protocol without ports has none.
//
// The side of a flow alone tells nothing, because a reply comes from the
// server. So the choice uses the ports, in this order:
//   - the port of a well-known service below the ephemeral range wins
//   - a privileged port wins over an unprivileged port
//   - a port below the ephemeral range wins over a port in it
//   - the port of a well-known service wins over a port without one
//   - else, the lower port wins
//
// A known port in the ephemeral range counts only late, because a client can
// pick it as its source port. A laptop that picks 51820 for a connection to a
// game server does not run WireGuard. A server that really listens there still
// wins over the random port of a client.
func servicePort(protocol uint8, src, dst uint16) uint16 {
	switch protocol {
	case 6, 17, 132: // TCP, UDP, SCTP
	default:
		return 0
	}

	s, d := traitsOf(src), traitsOf(dst)

	// Each rule decides only when the two ports differ on it. It picks the
	// port that has it. The first rule that decides wins.
	for _, rule := range [...]struct{ src, dst bool }{
		{s.settled(), d.settled()},   // a well-known service below the ephemeral range
		{s.privileged, d.privileged}, // a privileged port
		{!s.ephemeral, !d.ephemeral}, // any port below the ephemeral range
		{s.known, d.known},           // a well-known service, even inside it
	} {
		if rule.src != rule.dst {
			if rule.src {
				return src
			}

			return dst
		}
	}

	return min(src, dst)
}

// portTraits are the properties of a port for servicePort. They are calculated
// once for each port, because servicePort runs for every flow under the lock
// of the recorder.
type portTraits struct {
	known      bool // a well-known service's port
	privileged bool // below 1024
	ephemeral  bool // in the range clients draw source ports from
}

func traitsOf(p uint16) portTraits {
	return portTraits{
		known:      scanner.ServiceName(p) != "",
		privileged: p < 1024,
		ephemeral:  p >= 32768,
	}
}

// settled is the port of a well-known service that a client would not pick as
// its own.
func (t portTraits) settled() bool { return t.known && !t.ephemeral }

// clampInt64 limits a counter to what SQLite can hold. No real counter reaches
// the limit in an hour. A corrupt export that claims to must not wrap to a
// negative value.
func clampInt64(v uint64) int64 {
	const maxInt64 = 1<<63 - 1
	if v > maxInt64 {
		return maxInt64
	}

	return int64(v)
}

// hourOf is the stored hour bucket of t. Traffic, attempts and probes are all
// kept for each UTC hour.
func hourOf(t time.Time) dbtype.Time {
	return dbtype.NewTime(t.UTC().Truncate(time.Hour))
}

// parseHour reads an hour that an aggregate query returned as text. The error
// names the value.
func parseHour(what, s string) (time.Time, error) {
	t, err := time.Parse(dbtype.Layout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s hour %q: %w", what, s, err)
	}

	return t, nil
}

// currentHolders maps every current address of a device to that device.
func (s *Store) currentHolders(ctx context.Context) (map[netip.Addr]int64, error) {
	rows, err := s.q.CurrentHolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("current holders: %w", err)
	}

	holders := make(map[netip.Addr]int64, len(rows))
	for _, r := range rows {
		holders[r.IP.Addr] = r.DeviceID
	}

	return holders, nil
}
