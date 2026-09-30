package poller

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// fakeReader answers Topology with what it holds.
type fakeReader struct {
	name string
	topo plugin.Topology
	err  error
}

func (f *fakeReader) Name() string                                      { return f.name }
func (f *fakeReader) Kind() dbtype.SourceKind                           { return dbtype.SourceRouter }
func (f *fakeReader) Topology(context.Context) (plugin.Topology, error) { return f.topo, f.err }

func newTopologyTask(t *testing.T, readers ...plugin.TopologyReader) (*Topology, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	store := inventory.New(conn, slog.New(slog.DiscardHandler))

	return NewTopology(slog.New(slog.DiscardHandler), store, time.Hour, readers...), conn
}

func seenOnce(port, mac string) plugin.Topology {
	return plugin.Topology{Seen: []plugin.Sighting{{Port: port, MAC: mac}}, ReadAt: time.Now()}
}

func TestTopologyDueInWithoutHistoryIsNow(t *testing.T) {
	t.Parallel()

	task, _ := newTopologyTask(t)
	assert.Equal(t, "topology_reader", task.Name())
	assert.Zero(t, task.DueIn(t.Context()))
}

func TestTopologyDueInResumesTheInterval(t *testing.T) {
	t.Parallel()

	task, _ := newTopologyTask(t, &fakeReader{name: "routeros:switch_a", topo: seenOnce("ether4", "00:00:5e:00:53:01")})
	require.NoError(t, task.Run(t.Context()))

	due := task.DueIn(t.Context())
	assert.Greater(t, due, 59*time.Minute)
	assert.LessOrEqual(t, due, time.Hour)
}

// One source failing leaves the others recorded, and a partial read is
// recorded with what it had.
func TestTopologyRunRecordsWhatAnswered(t *testing.T) {
	t.Parallel()

	partial := seenOnce("ether2", "00:00:5e:00:53:02")

	task, conn := newTopologyTask(t,
		&fakeReader{name: "routeros:switch_a", topo: seenOnce("ether4", "00:00:5e:00:53:01")},
		&fakeReader{name: "routeros:ap", err: errors.New("unreachable")},
		&fakeReader{name: "routeros:gateway", topo: partial, err: errors.New("neighbours timed out")},
	)

	err := task.Run(t.Context())
	require.Error(t, err)
	assert.ErrorContains(t, err, "routeros:ap")
	assert.NotContains(t, err.Error(), "routeros:gateway")

	var n int
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM topology_nodes`).Scan(&n))
	assert.Equal(t, 2, n)
}
