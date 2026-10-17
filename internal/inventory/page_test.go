package inventory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// walkEvents pages the change log one page at a time. It returns the ids that
// it saw, in order. These tell whether the walk repeated or dropped something.
func walkEvents(t *testing.T, s *Store, limit int) []int64 {
	t.Helper()

	var (
		ids  []int64
		next Cursor
	)

	for range 20 {
		page, err := s.ListEvents(t.Context(), Page{Limit: limit, Cursor: next})
		require.NoError(t, err)

		for _, e := range page.Events {
			ids = append(ids, e.ID)
		}

		if page.Next.IsZero() {
			return ids
		}

		next = page.Next
	}

	t.Fatal("the walk did not reach the end of the log")

	return nil
}

// A walk in pages sees the same rows, in the same order, as a single read of
// the whole log.
func TestEventPagesWalkTheWholeLog(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"), host("192.0.2.11", macB, "nas.local"))

	whole, err := s.ListEvents(t.Context(), Page{Limit: 100})
	require.NoError(t, err)
	require.Greater(t, len(whole.Events), 2, "the sweep has to write enough events to page")
	require.True(t, whole.Next.IsZero(), "one page holds the whole log")

	want := make([]int64, 0, len(whole.Events))
	for _, e := range whole.Events {
		want = append(want, e.ID)
	}

	// Each page size divides the log differently. None of them can change
	// which rows come back, or their order.
	for _, limit := range []int{1, 2, 3} {
		assert.Equal(t, want, walkEvents(t, s, limit), "walking %d at a time", limit)
	}
}

// The last page says that there is nothing after it. This stops a client.
func TestLastPageHasNoCursor(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	whole, err := s.ListEvents(t.Context(), Page{Limit: 100})
	require.NoError(t, err)

	// A page that holds exactly the whole log must still report the end. The
	// read of one more row than the page holds exists for this.
	exact, err := s.ListEvents(t.Context(), Page{Limit: len(whole.Events)})
	require.NoError(t, err)

	assert.Len(t, exact.Events, len(whole.Events))
	assert.True(t, exact.Next.IsZero(), "the log ended, so there is no page after it")
}

// For this reason, the log does not page by offset. Rows arrive at the top
// while a client is in the middle. An offset counts from a top that moved. So
// the row on the boundary comes twice, and the next row never comes.
func TestPagingIsStableWhileTheLogGrows(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"), host("192.0.2.11", macB, "nas.local"))

	first, err := s.ListEvents(t.Context(), Page{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Events, 2)
	require.False(t, first.Next.IsZero())

	// A second sweep writes newer events. They sort before everything that the
	// first page returned.
	sweep(t, s, host("192.0.2.11", macB, "renamed.local"))

	second, err := s.ListEvents(t.Context(), Page{Limit: 2, Cursor: first.Next})
	require.NoError(t, err)
	require.NotEmpty(t, second.Events)

	seen := map[int64]bool{}
	for _, e := range first.Events {
		seen[e.ID] = true
	}

	for _, e := range second.Events {
		assert.False(t, seen[e.ID], "event %d was already on the first page", e.ID)
		assert.Less(t, e.ID, first.Events[len(first.Events)-1].ID,
			"the second page resumes below the row the first ended on")
	}
}

func TestScanPagesWalkTheWholeHistory(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	for range 3 {
		sweep(t, s, host("192.0.2.10", macA, ""))
	}

	whole, err := s.ListScans(t.Context(), Page{Limit: 100})
	require.NoError(t, err)
	require.Len(t, whole.Scans, 3)
	require.True(t, whole.Next.IsZero())

	var (
		ids  []int64
		next Cursor
	)

	for range 5 {
		page, err := s.ListScans(t.Context(), Page{Limit: 1, Cursor: next})
		require.NoError(t, err)
		require.Len(t, page.Scans, 1)

		ids = append(ids, page.Scans[0].ID)

		if page.Next.IsZero() {
			break
		}

		next = page.Next
	}

	want := []int64{whole.Scans[0].ID, whole.Scans[1].ID, whole.Scans[2].ID}
	assert.Equal(t, want, ids)
}

// Every event of one sweep has the same timestamp. So a cursor with only the
// timestamp would continue before rows that it already returned.
func TestCursorBreaksTiesWithinOneSweep(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"), host("192.0.2.11", macB, "nas.local"))

	whole, err := s.ListEvents(t.Context(), Page{Limit: 100})
	require.NoError(t, err)
	require.Greater(t, len(whole.Events), 1)

	// The premise: the rows that the walk must separate share a timestamp.
	assert.Equal(t, whole.Events[0].At, whole.Events[1].At,
		"one sweep stamps every event it writes alike")

	first, err := s.ListEvents(t.Context(), Page{Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Events, 1)

	next, err := s.ListEvents(t.Context(), Page{Limit: 1, Cursor: first.Next})
	require.NoError(t, err)
	require.Len(t, next.Events, 1)

	assert.NotEqual(t, first.Events[0].ID, next.Events[0].ID)
	assert.Equal(t, whole.Events[1].ID, next.Events[0].ID)
}
