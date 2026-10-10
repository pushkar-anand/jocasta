package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// minPrune is the fewest entries an attemptLimiter holds before it looks for
// ones to drop. Below it a sweep costs more than the memory it frees.
const minPrune = 1024

// attemptLimiter holds a separate allowance of attempts for each key, such as
// an account. Each key starts with burst attempts and gets one back every
// refill, up to burst again. It lives in memory, so a restart restores every
// allowance. An attemptLimiter is safe for concurrent use.
type attemptLimiter[K comparable] struct {
	burst  int
	refill time.Duration

	mu   sync.Mutex
	keys map[K]*rate.Limiter

	// pruneAt is the number of entries at which the next new key first drops
	// every entry back at a full allowance. It doubles past what a sweep
	// leaves, so sweeps stay rare however many keys are live.
	pruneAt int
}

func newAttemptLimiter[K comparable](burst int, refill time.Duration) *attemptLimiter[K] {
	return &attemptLimiter[K]{
		burst:   burst,
		refill:  refill,
		keys:    make(map[K]*rate.Limiter),
		pruneAt: minPrune,
	}
}

// spend takes one attempt from key's allowance at now. ok reports whether one
// was left to take, and last whether that was the final one, so the caller
// can stop asking before the next attempt is refused.
//
// A key whose allowance has refilled to full is dropped once enough keys
// accumulate, which a later attempt cannot tell from a key never seen. That
// keeps keys a caller cannot bound, such as client addresses, from growing
// the limiter without end.
func (l *attemptLimiter[K]) spend(key K, now time.Time) (ok, last bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	lim, found := l.keys[key]
	if !found {
		if len(l.keys) >= l.pruneAt {
			l.prune(now)
		}

		lim = rate.NewLimiter(rate.Every(l.refill), l.burst)
		l.keys[key] = lim
	}

	if !lim.AllowN(now, 1) {
		return false, false
	}

	return true, lim.TokensAt(now) < 1
}

// prune drops every entry back at a full allowance at now. l.mu must be held.
func (l *attemptLimiter[K]) prune(now time.Time) {
	for key, lim := range l.keys {
		if lim.TokensAt(now) >= float64(l.burst) {
			delete(l.keys, key)
		}
	}

	l.pruneAt = max(minPrune, 2*len(l.keys))
}
