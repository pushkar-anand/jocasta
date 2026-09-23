package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// problemOf decodes the problem document a failed tool call carries as its
// text.
func problemOf(t *testing.T, res *mcpsdk.CallToolResult) map[string]any {
	t.Helper()

	require.True(t, res.IsError, "the call should have failed")
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok, "the failure should be text, got %T", res.Content[0])

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(text.Text), &doc), "the failure should be a problem document: %s", text.Text)

	return doc
}

// failingTool registers a tool, through addTool, whose handler returns err.
func failingTool(name string, err error) func(*mcpsdk.Server, *slog.Logger) {
	return func(s *mcpsdk.Server, log *slog.Logger) {
		addTool(s, log, &mcpsdk.Tool{Name: name}, func(
			context.Context, *mcpsdk.CallToolRequest, struct{},
		) (*mcpsdk.CallToolResult, any, error) {
			return nil, nil, err
		})
	}
}

func callFailing(t *testing.T, cs *mcpsdk.ClientSession, name string) map[string]any {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: name})
	require.NoError(t, err)

	return problemOf(t, res)
}

// An error the API maps to a problem of its own is described the same way
// here, with the tool standing where the API puts the request path.
func TestToolErrorTheAPIRecognisesIsItsProblem(t *testing.T) {
	t.Parallel()

	cs := connect(t, failingTool("find", fmt.Errorf("device 7: %w", inventory.ErrNotFound)))

	doc := callFailing(t, cs, "find")

	assert.Equal(t, map[string]any{
		"type":     "about:blank",
		"title":    "Not Found",
		"status":   float64(http.StatusNotFound),
		"detail":   "device 7: not found",
		"instance": "find",
	}, doc)
}

// An unexpected error is logged and answered with a bare 500: its text can
// name storage internals, which are the server log's business, not the
// agent's.
func TestToolErrorThatIsUnexpectedIsLoggedNotShown(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer

	log := slog.New(slog.NewTextHandler(&logged, nil))
	cause := errors.New("sql: database table devices is locked")

	cs := connectLogging(t, log, failingTool("broken", cause))

	doc := callFailing(t, cs, "broken")

	assert.Equal(t, float64(http.StatusInternalServerError), doc["status"])
	assert.Equal(t, "Internal Server Error", doc["detail"])
	assert.NotContains(t, fmt.Sprint(doc), "sql")

	assert.Contains(t, logged.String(), "tool=broken")
	assert.Contains(t, logged.String(), cause.Error())
}
