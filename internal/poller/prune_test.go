package poller

import (
	"log/slog"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPruneTask(t *testing.T) *Prune {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	store := inventory.New(conn, slog.New(slog.DiscardHandler))

	return NewPrune(slog.New(slog.DiscardHandler), store, inventory.DefaultRetention, inventory.DefaultTrafficRetention)
}

func TestPruneSchedule(t *testing.T) {
	t.Parallel()

	p := newPruneTask(t)

	assert.Equal(t, "pruner", p.Name())
	assert.Equal(t, time.Hour, p.Interval())
	assert.Zero(t, p.DueIn(t.Context()), "a prune has no schedule to resume")
}

func TestPruneRunsOnAnEmptyStore(t *testing.T) {
	t.Parallel()

	require.NoError(t, newPruneTask(t).Run(t.Context()))
}
