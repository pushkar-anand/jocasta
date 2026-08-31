package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	res, body := get(t, seeded(t), "/livez")

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", res.Header.Get("Content-Type"))

	// The value is whatever the build stamped in, which is empty for a test
	// binary, so only the shape of the payload is worth asserting.
	require.Contains(t, body, "version")
	assert.Len(t, body, 1)
}
