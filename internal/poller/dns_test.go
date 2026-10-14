package poller

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeQueryReader hands back the cursor it was given with a mark added, and
// remembers each cursor it was handed.
type fakeQueryReader struct {
	cursors []string
	err     error
}

func (f *fakeQueryReader) Name() string            { return "technitium:resolver" }
func (f *fakeQueryReader) Kind() dbtype.SourceKind { return dbtype.SourceDNS }

func (f *fakeQueryReader) Queries(_ context.Context, cursor string) ([]plugin.Query, string, error) {
	f.cursors = append(f.cursors, cursor)

	if f.err != nil {
		return nil, cursor, f.err
	}

	return nil, cursor + "+", nil
}

func newDNSTask(t *testing.T, r plugin.QueryReader) *DNS {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return NewDNS(slog.New(slog.DiscardHandler), inventory.New(conn, slog.New(slog.DiscardHandler)), r)
}

// TestDNSResumesFromTheStoredCursor checks that each run hands the source the
// cursor the last run stored.
func TestDNSResumesFromTheStoredCursor(t *testing.T) {
	t.Parallel()

	r := &fakeQueryReader{}
	d := newDNSTask(t, r)

	assert.Equal(t, "dns_reader", d.Name())
	assert.Equal(t, DNSInterval, d.Interval())

	for range 3 {
		require.NoError(t, d.Run(t.Context()))
	}

	assert.Equal(t, []string{"", "+", "++"}, r.cursors)
}

// TestDNSReportsAFailedRead checks that a read returning nothing is an error
// and leaves the stored cursor where it was.
func TestDNSReportsAFailedRead(t *testing.T) {
	t.Parallel()

	r := &fakeQueryReader{}
	d := newDNSTask(t, r)

	require.NoError(t, d.Run(t.Context()))

	r.err = errors.New("server down")

	require.ErrorContains(t, d.Run(t.Context()), "server down")

	r.err = nil

	require.NoError(t, d.Run(t.Context()))

	assert.Equal(t, []string{"", "+", "+"}, r.cursors)
}
