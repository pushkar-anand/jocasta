package inventory

import (
	"net/netip"
	"sync"
	"time"
)

// recentWindow is how much activity the recorder keeps in memory. The tables
// hold hours; this is what "active just now" is read from, and it is gone
// after a restart.
const recentWindow = 15 * time.Minute

// RecentEdge is the traffic between two addresses over the recent window, both
// directions together. A is the lower of the two.
type RecentEdge struct {
	A, B  netip.Addr
	Bytes uint64

	// Last is the flush that last carried traffic between them.
	Last time.Time
}

// recentPair is two addresses in a fixed order, so both directions of a
// conversation meet on one key.
type recentPair struct {
	a, b netip.Addr
}

func pairOfAddrs(x, y netip.Addr) recentPair {
	if y.Less(x) {
		x, y = y, x
	}

	return recentPair{a: x, b: y}
}

// recentBucket is what one flush carried.
type recentBucket struct {
	at    time.Time
	bytes map[recentPair]uint64
}

// recentActivity is a short run of flushes, oldest first.
type recentActivity struct {
	mu      sync.RWMutex
	buckets []recentBucket
}

// push adds one flush's conversations and drops the flushes that have left
// the window. Attempts are not passed in: nothing came of them.
func (ra *recentActivity) push(at time.Time, conversations map[trafficKey]*trafficTotals) {
	b := recentBucket{at: at, bytes: make(map[recentPair]uint64, len(conversations))}

	for k, t := range conversations {
		b.bytes[pairOfAddrs(k.src, k.dst)] += t.bytes
	}

	ra.mu.Lock()
	defer ra.mu.Unlock()

	keep := ra.buckets[:0]

	for _, old := range ra.buckets {
		if at.Sub(old.at) < recentWindow {
			keep = append(keep, old)
		}
	}

	if len(b.bytes) > 0 {
		keep = append(keep, b)
	}

	ra.buckets = keep
}

// since returns the edges with traffic in a flush after a moment.
func (ra *recentActivity) since(t time.Time) []RecentEdge {
	ra.mu.RLock()
	defer ra.mu.RUnlock()

	edges := make(map[recentPair]*RecentEdge)

	for _, b := range ra.buckets {
		if !b.at.After(t) {
			continue
		}

		for p, n := range b.bytes {
			e, ok := edges[p]
			if !ok {
				e = &RecentEdge{A: p.a, B: p.b}
				edges[p] = e
			}

			e.Bytes += n
			e.Last = b.at
		}
	}

	out := make([]RecentEdge, 0, len(edges))
	for _, e := range edges {
		out = append(out, *e)
	}

	return out
}

// Recent returns the traffic between each pair of addresses in the flushes
// after a moment, as far back as the recent window reaches. A flow to the
// router's outside address is under the router's own address, as it is in
// the tables. It is safe to call while the recorder flushes.
func (r *TrafficRecorder) Recent(since time.Time) []RecentEdge {
	return r.recent.since(since)
}
