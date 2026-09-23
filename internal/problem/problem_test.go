package problem

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	t.Run("a missing record is a 404 naming it", func(t *testing.T) {
		t.Parallel()

		p, ok := Resolve(fmt.Errorf("device 7: %w", inventory.ErrNotFound))
		require.True(t, ok)
		assert.Equal(t, http.StatusNotFound, p.Status())
		assert.Equal(t, "device 7: not found", p.Detail())
	})

	t.Run("an error that is already a problem stays one", func(t *testing.T) {
		t.Parallel()

		teapot := response.NewProblem().WithStatus(http.StatusTeapot).WithDetail("short and stout").Build()

		p, ok := Resolve(fmt.Errorf("wrapped: %w", teapot))
		require.True(t, ok)
		assert.Equal(t, http.StatusTeapot, p.Status())
	})

	t.Run("anything else is unrecognised", func(t *testing.T) {
		t.Parallel()

		_, ok := Resolve(errors.New("disk on fire"))
		assert.False(t, ok)
	})
}

// Document lays a problem out the way response.JSONWriter does, so the API
// and MCP send the same members.
func TestDocument(t *testing.T) {
	t.Parallel()

	t.Run("about:blank takes the status text as its title", func(t *testing.T) {
		t.Parallel()

		p := response.NewProblem().WithStatus(http.StatusNotFound).WithTitle("ignored").WithDetail("device 7: not found").Build()

		assert.Equal(t, map[string]any{
			"type":     "about:blank",
			"title":    "Not Found",
			"status":   http.StatusNotFound,
			"detail":   "device 7: not found",
			"instance": "list_devices",
		}, Document(p, "list_devices"))
	})

	t.Run("a typed problem keeps its title and custom members", func(t *testing.T) {
		t.Parallel()

		p := response.NewProblem().
			WithType("https://example.com/problems/busy").
			WithTitle("Busy").
			WithStatus(http.StatusServiceUnavailable).
			WithCustomMember("retry_after", 5).
			Build()

		doc := Document(p, "x")
		assert.Equal(t, "https://example.com/problems/busy", doc["type"])
		assert.Equal(t, "Busy", doc["title"])
		assert.Equal(t, 5, doc["retry_after"])
	})

	t.Run("Internal says only that something failed", func(t *testing.T) {
		t.Parallel()

		doc := Document(Internal(), "x")
		assert.Equal(t, http.StatusInternalServerError, doc["status"])
		assert.Equal(t, "Internal Server Error", doc["detail"])
	})
}
