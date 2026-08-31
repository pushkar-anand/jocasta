package api

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListEventsAndScans(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	status, _, body := get(t, h, "/events")
	require.Equal(t, http.StatusOK, status)
	assert.NotEmpty(t, list(t, body, "events"))

	status, _, body = get(t, h, "/scans")
	require.Equal(t, http.StatusOK, status)

	scans := list(t, body, "scans")
	require.Len(t, scans, 1)

	scan, ok := scans[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-sweep", scan["source"])
	assert.Equal(t, prefix, scan["network"])
	assert.Equal(t, "OK", scan["status"])
}

// A client walks the log by handing back the token the last page gave it, and
// sees every row exactly once.
func TestPagingWalksTheLogWithCursors(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	_, _, whole := get(t, h, "/events")
	total := len(list(t, whole, "events"))
	require.Greater(t, total, 2, "the fixture has to write enough events to page")
	assert.NotContains(t, whole, "next_cursor", "one page held the whole log")

	var (
		seen   []any
		cursor string
	)

	for range total + 1 {
		target := "/events?limit=1"
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}

		status, _, body := get(t, h, target)
		require.Equal(t, http.StatusOK, status)

		seen = append(seen, list(t, body, "events")...)

		next, ok := body["next_cursor"]
		if !ok {
			break
		}

		cursor, ok = next.(string)
		require.True(t, ok, "next_cursor should be a string, got %T", next)
		require.NotEmpty(t, cursor)
	}

	// The walk saw the same rows, in the same order, as the single read.
	assert.Equal(t, list(t, whole, "events"), seen)
}

// The token is opaque: a client that did not get it from a page cannot make one.
func TestPagingRejectsAMalformedCursor(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, target := range []string{"/events?cursor=!!!", "/scans?cursor=bm90LWEtY3Vyc29y"} {
		t.Run(target, func(t *testing.T) {
			status, _, _ := get(t, h, target)
			assert.Equal(t, http.StatusBadRequest, status)
		})
	}
}

// An empty cursor is the first page rather than a failure, so a client need not
// leave the parameter off to start.
func TestEmptyCursorIsTheFirstPage(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	status, _, body := get(t, h, "/events?cursor=")
	require.Equal(t, http.StatusOK, status)
	assert.NotEmpty(t, list(t, body, "events"))
}

// Offset paging is gone, so the parameter it used is not one the API takes.
func TestOffsetIsNotAParameter(t *testing.T) {
	t.Parallel()

	status, _, _ := get(t, seeded(t), "/events?offset=1")
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestPageSizeIsBounded(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	status, _, body := get(t, h, "/events?limit=1")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, list(t, body, "events"), 1)

	// A limit that is not a number never becomes one, so it is malformed;
	// a limit that is a number but out of range is understood and refused.
	status, _, _ = get(t, h, "/events?limit=0.5")
	assert.Equal(t, http.StatusBadRequest, status)

	tests := []struct{ target, field string }{
		{"/events?limit=-1", "limit"},
		{"/events?limit=99999", "limit"},
		{"/scans?limit=99999", "limit"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			status, _, body := get(t, h, tc.target)

			require.Equal(t, http.StatusUnprocessableEntity, status)
			assert.Contains(t, problemContext(t, body), tc.field)
		})
	}
}
