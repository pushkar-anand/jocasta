package inventory

import (
	"database/sql"
	"errors"
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

// Each task keeps one row. A later run replaces it.
func TestRecordRunKeepsTheLatestRunPerTask(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	first := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	other := first.Add(time.Minute)

	require.NoError(t, s.RecordRun(t.Context(), "sweeper", first, nil))
	require.NoError(t, s.RecordRun(t.Context(), "sweeper", second, nil))
	require.NoError(t, s.RecordRun(t.Context(), "pruner", other, nil))

	got, err := s.LastRun(t.Context(), "sweeper")
	require.NoError(t, err)
	assert.True(t, second.Equal(got), got)

	got, err = s.LastRun(t.Context(), "pruner")
	require.NoError(t, err)
	assert.True(t, other.Equal(got), got)
}

// A failed run stores the reason. The next successful run clears it.
func TestRecordRunKeepsTheLatestRunsError(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

	runErr := func() sql.NullString {
		var msg sql.NullString
		require.NoError(t, conn.QueryRowContext(t.Context(),
			`SELECT error FROM task_runs WHERE name = 'sweeper'`).Scan(&msg))

		return msg
	}

	require.NoError(t, s.RecordRun(t.Context(), "sweeper", at, errors.New("sweep 192.0.2.0/24: boom")))
	assert.Equal(t, sql.NullString{String: "sweep 192.0.2.0/24: boom", Valid: true}, runErr())

	require.NoError(t, s.RecordRun(t.Context(), "sweeper", at.Add(time.Hour), nil))
	assert.False(t, runErr().Valid, "a run that succeeded must clear the last failure")
}
