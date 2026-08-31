package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListEventsAndScans(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	res, body := get(t, h, "/events")
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.NotEmpty(t, list(t, body, "events"))

	res, body = get(t, h, "/scans")
	require.Equal(t, http.StatusOK, res.StatusCode)

	scans := list(t, body, "scans")
	require.Len(t, scans, 1)

	scan, ok := scans[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-sweep", scan["source"])
	assert.Equal(t, prefix, scan["network"])
	assert.Equal(t, "OK", scan["status"])
}

func TestPagingIsBounded(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	res, body := get(t, h, "/events?limit=1")
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Len(t, list(t, body, "events"), 1)

	// A limit that is not a number never becomes one, so it is malformed;
	// a limit that is a number but out of range is understood and refused.
	res, _ = get(t, h, "/events?limit=0.5")
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)

	tests := []struct{ target, field string }{
		{"/events?limit=-1", "limit"},
		{"/events?limit=99999", "limit"},
		{"/events?offset=-1", "offset"},
		{"/scans?limit=99999", "limit"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			res, body := get(t, h, tc.target)

			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			assert.Contains(t, problemContext(t, body), tc.field)
		})
	}
}
