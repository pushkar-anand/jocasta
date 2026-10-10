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

	// pruneAt is the entry count at which the next new key sets off a prune.
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
// Memory stays bounded however many keys arrive, so a key may be one a caller
// cannot bound, such as a client address.
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

// prune drops every entry back at a full allowance at now, which a later
// attempt cannot tell from a key never seen. l.mu must be held.
func (l *attemptLimiter[K]) prune(now time.Time) {
	for key, lim := range l.keys {
		if lim.TokensAt(now) >= float64(l.burst) {
			delete(l.keys, key)
		}
	}

	// Twice what is left, so prunes stay rare however many keys are live.
	l.pruneAt = max(minPrune, 2*len(l.keys))
}
