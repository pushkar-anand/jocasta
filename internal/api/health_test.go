package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	status, headers, body := get(t, seeded(t), "/livez")

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "application/json; charset=utf-8", headers.Get("Content-Type"))

	// The value is whatever the build stamped in, which is the "dev"
	// placeholder for a test binary, so only the shape of the payload is worth
	// asserting.
	require.Contains(t, body, "version")
	assert.Len(t, body, 1)
}
