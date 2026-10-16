package auth

import (
	"maps"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// minPrune is the smallest number of entries in an attemptLimiter before it
// looks for entries to remove. Below it, a sweep costs more than the memory
// that it frees.
const minPrune = 1024

// attemptLimiter holds a separate number of attempts for each key, such as an
// account. Each key starts with burst attempts, and gets one back every
// refill, up to burst again. It is in memory, so a restart restores every
// limit. An attemptLimiter is safe for concurrent use.
type attemptLimiter[K comparable] struct {
	burst  int
	refill time.Duration

	mu   sync.Mutex
	keys map[K]*rate.Limiter

	// pruneAt is the entry count at which the next new key starts a prune.
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

// spend takes one attempt from the limit of key at now. ok reports whether an
// attempt was left. last reports whether it was the final one. So the caller
// can stop before the next attempt is refused.
//
// Memory stays limited for any number of keys. So a key can be a value that a
// caller cannot limit, such as a client address.
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

// prune removes every entry that is back at its full limit at now. A later
// attempt cannot tell such an entry from a new key. l.mu must be held.
func (l *attemptLimiter[K]) prune(now time.Time) {
	maps.DeleteFunc(l.keys, func(_ K, lim *rate.Limiter) bool {
		return lim.TokensAt(now) >= float64(l.burst)
	})

	// Twice what is left, so prunes stay rare for any number of live keys.
	l.pruneAt = max(minPrune, 2*len(l.keys))
}
