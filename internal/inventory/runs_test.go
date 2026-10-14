package inventory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLastRunWithoutAnyIsNotFound(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	_, err := s.LastRun(t.Context(), "sweeper")
	require.ErrorIs(t, err, ErrNotFound)
}

// Each task keeps one row, and a later run replaces it.
func TestRecordRunKeepsTheLatestRunPerTask(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	first := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	other := first.Add(time.Minute)

	require.NoError(t, s.RecordRun(t.Context(), "sweeper", first))
	require.NoError(t, s.RecordRun(t.Context(), "sweeper", second))
	require.NoError(t, s.RecordRun(t.Context(), "pruner", other))

	got, err := s.LastRun(t.Context(), "sweeper")
	require.NoError(t, err)
	assert.True(t, second.Equal(got), got)

	got, err = s.LastRun(t.Context(), "pruner")
	require.NoError(t, err)
	assert.True(t, other.Equal(got), got)
}
