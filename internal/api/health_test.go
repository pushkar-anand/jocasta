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

	// The value comes from the build. For a test binary, it is the "dev"
	// placeholder. So the test asserts only the shape of the payload.
	assert.Contains(t, body, "version")
	assert.Contains(t, body, "time")
	assert.Len(t, body, 2)
}
