package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestShelfGivesASecretOutOnce(t *testing.T) {
	t.Parallel()

	s := newShelf(time.Now)
	id := s.put("jct_placeholder")

	got, ok := s.take(id)
	assert.True(t, ok)
	assert.Equal(t, "jct_placeholder", got)

	_, ok = s.take(id)
	assert.False(t, ok, "a second take finds nothing")
}

func TestShelfFindsNothingForAnUnknownID(t *testing.T) {
	t.Parallel()

	s := newShelf(time.Now)
	s.put("jct_placeholder")

	_, ok := s.take("")
	assert.False(t, ok)

	_, ok = s.take("unknown")
	assert.False(t, ok)
}

func TestShelfDropsASecretNotTakenInTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newShelf(func() time.Time { return now })

	kept := s.put("jct_kept")
	late := s.put("jct_late")

	now = now.Add(shelfTTL - time.Second)

	_, ok := s.take(kept)
	assert.True(t, ok, "a secret taken inside its time is there")

	now = now.Add(2 * time.Second)

	_, ok = s.take(late)
	assert.False(t, ok, "a secret not taken in time is gone")
}

// A put sweeps out what expired, so secrets nobody comes back for do not
// pile up in memory.
func TestShelfSweepsExpiredSecretsOnPut(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newShelf(func() time.Time { return now })

	s.put("jct_abandoned")

	now = now.Add(shelfTTL + time.Second)

	s.put("jct_fresh")
	assert.Len(t, s.entries, 1)
}
