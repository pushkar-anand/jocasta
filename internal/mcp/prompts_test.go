package mcp

import (
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectPrompt joins a client to a server holding only the given prompt,
// registered as it would be on the read or the read_write server.
func connectPrompt(t *testing.T, p prompt, canWrite bool) *mcpsdk.ClientSession {
	t.Helper()

	return connect(t, func(s *mcpsdk.Server, _ *slog.Logger) { p(s, canWrite) })
}

// promptText gets a prompt and returns the text of its one message.
func promptText(t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]string) string {
	t.Helper()

	res, err := cs.GetPrompt(t.Context(), &mcpsdk.GetPromptParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.Len(t, res.Messages, 1)
	assert.Equal(t, mcpsdk.Role("user"), res.Messages[0].Role)

	text, ok := res.Messages[0].Content.(*mcpsdk.TextContent)
	require.True(t, ok, "prompt content should be text, got %T", res.Messages[0].Content)

	return text.Text
}

func TestTriageDevices(t *testing.T) {
	t.Parallel()

	t.Run("read_write applies what the owner confirms", func(t *testing.T) {
		t.Parallel()

		text := promptText(t, connectPrompt(t, triageDevices, true), "triage_devices", nil)
		assert.Contains(t, text, "update_device_curation")
		assert.Contains(t, text, "apply only the ones I confirm")
		assert.Contains(t, text, dataNotInstructions)
	})

	t.Run("read says it cannot apply", func(t *testing.T) {
		t.Parallel()

		text := promptText(t, connectPrompt(t, triageDevices, false), "triage_devices", nil)
		assert.NotContains(t, text, "update_device_curation", "a read session is never offered the tool")
		assert.Contains(t, text, "can only read")
		assert.Contains(t, text, dataNotInstructions)
	})
}

func TestWeeklyReport(t *testing.T) {
	t.Parallel()

	now := func() time.Time { return time.Date(2026, 3, 15, 12, 30, 45, 500, time.FixedZone("test", 3600)) }
	cs := connectPrompt(t, weeklyReport(now), false)

	t.Run("a week by default", func(t *testing.T) {
		t.Parallel()

		text := promptText(t, cs, "weekly_report", nil)
		assert.Contains(t, text, "in the 7 days since 2026-03-08T11:30:45Z, up to 2026-03-15T11:30:45Z")
		assert.Contains(t, text, "occurred before 2026-03-08T11:30:45Z")
		assert.Contains(t, text, "list_traffic with first_contact_only true and days 7")
		assert.Contains(t, text, dataNotInstructions)
	})

	t.Run("the days asked for", func(t *testing.T) {
		t.Parallel()

		text := promptText(t, cs, "weekly_report", map[string]string{"days": "30"})
		assert.Contains(t, text, "in the 30 days since 2026-02-13T11:30:45Z")
	})

	for _, days := range []string{"0", "91", "-1", "seven", "7.5"} {
		t.Run("refuses "+days, func(t *testing.T) {
			t.Parallel()

			_, err := cs.GetPrompt(t.Context(), &mcpsdk.GetPromptParams{
				Name:      "weekly_report",
				Arguments: map[string]string{"days": days},
			})

			rpcErr, ok := errors.AsType[*jsonrpc.Error](err)
			require.True(t, ok, "want a JSON-RPC error, got %v", err)
			assert.Equal(t, int64(jsonrpc.CodeInvalidParams), rpcErr.Code)
			assert.Contains(t, rpcErr.Message, "days must be a whole number from 1 to 90")
		})
	}
}

// Both servers offer every prompt, and the triage prompt a token is handed
// matches what its scope lets the model do.
func TestHandlerOffersPrompts(t *testing.T) {
	t.Parallel()

	a, tok := testAuth(t)

	srv := httptest.NewServer(NewHandler(testLogger(), testJSONWriter(), a, seededStore(t)))
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		name     string
		token    string
		canWrite bool
	}{
		{"read", tok.read, false},
		{"read_write", tok.readWrite, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cs := connectHTTP(t, srv.URL, tc.token)

			res, err := cs.ListPrompts(t.Context(), nil)
			require.NoError(t, err)

			names := make([]string, 0, len(res.Prompts))
			for _, p := range res.Prompts {
				names = append(names, p.Name)
			}

			assert.ElementsMatch(t, []string{"triage_devices", "weekly_report"}, names)

			text := promptText(t, cs, "triage_devices", nil)
			assert.Equal(t, tc.canWrite, strings.Contains(text, "update_device_curation"))
		})
	}
}
