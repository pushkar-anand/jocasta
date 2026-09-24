package inventory

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// Both directions of a conversation are one edge, with the flush that last
// carried it.
func TestRecentMergesBothDirections(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	at := s.now()
	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, exported(
		flow("192.0.2.11", "192.0.2.10", 51000, 22, 1_000, at),
		flow("192.0.2.10", "192.0.2.11", 22, 51000, 3_000, at),
	))
	require.NoError(t, rec.Flush(t.Context()))

	got := rec.Recent(at.Add(-time.Minute))
	require.Len(t, got, 1)
	assert.Equal(t, netip.MustParseAddr("192.0.2.10"), got[0].A)
	assert.Equal(t, netip.MustParseAddr("192.0.2.11"), got[0].B)
	assert.Equal(t, uint64(4_000), got[0].Bytes)
	assert.True(t, got[0].Last.After(at))
}

// A knock nothing came of is not activity.
func TestRecentLeavesOutAttempts(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	at := s.now()
	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, knock("192.0.2.10", "192.0.2.11", 23, "closed", at))
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, rec.Recent(at.Add(-time.Minute)))
}

// A flush leaves the window once it is older than it, and a caller asking
// about a later moment does not see earlier flushes.
func TestRecentForgetsOldFlushes(t *testing.T) {
	t.Parallel()

	s, _, advance := clockStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""), host("192.0.2.12", "00:00:5e:00:53:03", ""))

	rec := newRecorder(s, nil)

	at := s.now()
	rec.Add(trafficSource{}, []plugin.Flow{flow("192.0.2.10", "192.0.2.11", 51000, 22, 1_000, at)})
	require.NoError(t, rec.Flush(t.Context()))

	advance(recentWindow)

	later := s.now()
	rec.Add(trafficSource{}, []plugin.Flow{flow("192.0.2.10", "192.0.2.12", 51000, 22, 1_000, later)})
	require.NoError(t, rec.Flush(t.Context()))

	got := rec.Recent(at.Add(-time.Minute))
	require.Len(t, got, 1, "the first flush has left the window")
	assert.Equal(t, netip.MustParseAddr("192.0.2.12"), got[0].B)

	assert.Empty(t, rec.Recent(s.now()))
}
