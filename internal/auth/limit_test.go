package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestAttemptLimiterPrunesFullAllowances covers the limiter dropping keys back
// at a full allowance once minPrune of them accumulate, while a key still
// short of attempts keeps its count.
func TestAttemptLimiterPrunesFullAllowances(t *testing.T) {
	t.Parallel()

	l := newAttemptLimiter[int](2, time.Minute)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for key := range minPrune {
		l.spend(key, now)
	}

	assert.Len(t, l.keys, minPrune)

	// Two refills later every key is full again. Key 0 spends one just
	// before the new key that sets off the sweep.
	now = now.Add(2 * time.Minute)
	l.spend(0, now)
	l.spend(minPrune, now)

	assert.Len(t, l.keys, 2, "only key 0 and the new key are short of a full allowance")

	ok, _ := l.spend(0, now)
	assert.True(t, ok)

	ok, _ = l.spend(0, now)
	assert.False(t, ok, "key 0 kept its count through the sweep")
}
