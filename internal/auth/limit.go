package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// attemptLimiter holds a separate allowance of attempts for each key, such as
// an account. Each key starts with burst attempts and gets one back every
// refill, up to burst again. It lives in memory, so a restart restores every
// allowance. An attemptLimiter is safe for concurrent use.
type attemptLimiter[K comparable] struct {
	burst  int
	refill time.Duration

	mu   sync.Mutex
	keys map[K]*rate.Limiter
}

func newAttemptLimiter[K comparable](burst int, refill time.Duration) *attemptLimiter[K] {
	return &attemptLimiter[K]{
		burst:  burst,
		refill: refill,
		keys:   make(map[K]*rate.Limiter),
	}
}

// spend takes one attempt from key's allowance at now. ok reports whether one
// was left to take, and last whether that was the final one, so the caller
// can stop asking before the next attempt is refused.
//
// A key's entry stays for the life of the process. Callers keep that bounded
// by choosing keys that cannot be invented at will.
func (l *attemptLimiter[K]) spend(key K, now time.Time) (ok, last bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	lim, found := l.keys[key]
	if !found {
		lim = rate.NewLimiter(rate.Every(l.refill), l.burst)
		l.keys[key] = lim
	}

	if !lim.AllowN(now, 1) {
		return false, false
	}

	return true, lim.TokensAt(now) < 1
}
