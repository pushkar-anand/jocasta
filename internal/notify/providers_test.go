package notify_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/notify"
)

// request is what a test server was sent.
type request struct {
	method string
	path   string
	header http.Header
	raw    []byte
	body   map[string]any
}

// server records the one request it is sent and answers with status.
func server(t *testing.T, status int) (*httptest.Server, *request) {
	t.Helper()

	got := &request{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		assert.NoError(t, err)

		got.method, got.path, got.header, got.raw = r.Method, r.URL.Path, r.Header.Clone(), raw
		assert.NoError(t, json.Unmarshal(raw, &got.body))

		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	return srv, got
}

func destination(t *testing.T, name string, c notify.Config) *notify.Destination {
	t.Helper()

	d, err := notify.NewDestination(name, c)
	require.NoError(t, err)

	return d
}

var msg = notify.Message{
	Title:  "1 new device on 192.0.2.0/24",
	Body:   "host-a · 192.0.2.10",
	ScanID: 7,
	Events: []*inventory.Event{
		{Kind: dbtype.EventDeviceDiscovered, DeviceID: 3, DeviceName: "host-a", NewValue: "192.0.2.10"},
	},
}

func TestNtfy(t *testing.T) {
	srv, got := server(t, http.StatusOK)

	d := destination(t, "phone", notify.Config{Ntfy: &notify.Ntfy{
		URL: srv.URL + "/jocasta", Token: "tk_placeholder", Priority: 4,
	}})

	assert.Equal(t, "phone", d.Name())
	assert.Equal(t, notify.KindNtfy, d.Kind())
	assert.Equal(t, strings.TrimPrefix(srv.URL, "http://"), d.Host())

	require.NoError(t, d.Send(t.Context(), msg))

	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/", got.path, "JSON is published to the server's root")
	assert.Equal(t, "Bearer tk_placeholder", got.header.Get("Authorization"))
	assert.Equal(t, map[string]any{
		"topic": "jocasta", "title": msg.Title, "message": msg.Body, "priority": float64(4),
	}, got.body)
}

// A server can sit under a path of its own; the topic is the last segment.
func TestNtfyUnderAPath(t *testing.T) {
	srv, got := server(t, http.StatusOK)

	d := destination(t, "phone", notify.Config{Ntfy: &notify.Ntfy{URL: srv.URL + "/ntfy/jocasta/"}})
	require.NoError(t, d.Send(t.Context(), msg))

	assert.Equal(t, "/ntfy/", got.path)
	assert.Equal(t, "jocasta", got.body["topic"])
	assert.Empty(t, got.header.Get("Authorization"))
	assert.NotContains(t, got.body, "priority")
}

func TestWebhookIsSigned(t *testing.T) {
	srv, got := server(t, http.StatusOK)

	d := destination(t, "automation", notify.Config{Webhook: &notify.Webhook{
		URL: srv.URL + "/jocasta", Secret: "placeholder-secret",
	}})
	assert.Equal(t, notify.KindWebhook, d.Kind())

	require.NoError(t, d.Send(t.Context(), msg))

	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/jocasta", got.path)

	// What a receiver does: recompute over the raw body and compare.
	mac := hmac.New(sha256.New, []byte("placeholder-secret"))
	mac.Write(got.raw)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	assert.True(t, hmac.Equal([]byte(want), []byte(got.header.Get(notify.HeaderSignature))))
	assert.NotEmpty(t, got.header.Get(notify.HeaderDelivery))

	assert.Equal(t, msg.Title, got.body["title"])
	assert.Equal(t, msg.Body, got.body["message"])
	assert.InDelta(t, 7, got.body["scan_id"], 0)
	assert.Equal(t, []any{map[string]any{
		"kind": "DEVICE_DISCOVERED", "device_id": float64(3), "device": "host-a", "change": "192.0.2.10",
	}}, got.body["events"])
}

func TestEachDeliveryHasItsOwnID(t *testing.T) {
	srv, got := server(t, http.StatusOK)
	d := destination(t, "automation", notify.Config{Webhook: &notify.Webhook{URL: srv.URL, Secret: "s"}})

	require.NoError(t, d.Send(t.Context(), msg))

	first := got.header.Get(notify.HeaderDelivery)

	require.NoError(t, d.Send(t.Context(), msg))
	assert.NotEqual(t, first, got.header.Get(notify.HeaderDelivery))
}

// A webhook URL can carry a token in its query; a failed send must not put it
// in the log.
func TestSendErrorsNameTheHostOnly(t *testing.T) {
	srv, _ := server(t, http.StatusUnauthorized)
	host := strings.TrimPrefix(srv.URL, "http://")

	d := destination(t, "automation", notify.Config{Webhook: &notify.Webhook{
		URL: srv.URL + "/jocasta?token=placeholder-token", Secret: "s",
	}})

	err := d.Send(t.Context(), msg)
	require.Error(t, err)
	assert.Equal(t, host+" answered 401 Unauthorized", err.Error())

	srv.Close()

	err = d.Send(t.Context(), msg)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "placeholder-token")
	assert.Contains(t, err.Error(), "could not reach "+host)
}

// A destination's timeout bounds a service that stops answering.
func TestTimeoutBoundsASlowService(t *testing.T) {
	// The server notices the client hang up only once it has read the body.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	d := destination(t, "slow", notify.Config{
		Timeout: 50 * time.Millisecond,
		Webhook: &notify.Webhook{URL: srv.URL, Secret: "s"},
	})

	start := time.Now()
	err := d.Send(t.Context(), msg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not reach")
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestOnByDefault(t *testing.T) {
	off := false

	assert.True(t, notify.Config{}.On())
	assert.False(t, notify.Config{Enabled: &off}.On())
}

func TestNewDestinationRejectsBadConfig(t *testing.T) {
	ntfy := func(n notify.Ntfy) notify.Config { return notify.Config{Ntfy: &n} }
	hook := func(w notify.Webhook) notify.Config { return notify.Config{Webhook: &w} }

	for name, c := range map[string]notify.Config{
		"no service":             {},
		"two services":           {Ntfy: &notify.Ntfy{URL: "https://ntfy.example.com/t"}, Webhook: &notify.Webhook{URL: "https://hooks.example.com", Secret: "s"}},
		"ntfy without url":       ntfy(notify.Ntfy{}),
		"ntfy without topic":     ntfy(notify.Ntfy{URL: "https://ntfy.example.com/"}),
		"ntfy priority":          ntfy(notify.Ntfy{URL: "https://ntfy.example.com/t", Priority: 6}),
		"ntfy scheme":            ntfy(notify.Ntfy{URL: "ftp://ntfy.example.com/t"}),
		"webhook without url":    hook(notify.Webhook{Secret: "s"}),
		"webhook relative":       hook(notify.Webhook{URL: "/jocasta", Secret: "s"}),
		"webhook without secret": hook(notify.Webhook{URL: "https://hooks.example.com/jocasta"}),
	} {
		_, err := notify.NewDestination("x", c)
		assert.Error(t, err, name)
	}
}
