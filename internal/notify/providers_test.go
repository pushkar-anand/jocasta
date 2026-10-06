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

// A redirect is not followed, so the headers a destination sets, such as a
// token, never reach the address it points to. The send fails and says what
// to change.
func TestRedirectIsNotFollowed(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the redirect was followed")
	}))
	t.Cleanup(elsewhere.Close)

	srv := httptest.NewServer(http.RedirectHandler(elsewhere.URL, http.StatusMovedPermanently))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")

	d := destination(t, "phone", notify.Config{Ntfy: &notify.Ntfy{
		URL: srv.URL + "/jocasta", Token: "tk_placeholder",
	}})

	err := d.Send(t.Context(), msg)
	require.Error(t, err)
	assert.Equal(t,
		host+" answered 301 Moved Permanently. Set the url to the address it redirects to",
		err.Error())
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

// Zero is a valid priority, so the error's range starts there.
func TestNtfyPriorityErrorNamesTheRange(t *testing.T) {
	err := (&notify.Ntfy{URL: "https://ntfy.example.com/t", Priority: 7}).Validate()
	require.Error(t, err)
	assert.Equal(t, "priority 7 is outside 0 to 5. Use 0 to leave it to the server", err.Error())

	require.NoError(t, (&notify.Ntfy{URL: "https://ntfy.example.com/t"}).Validate())
}

func TestHTTPShapesTheBody(t *testing.T) {
	srv, got := server(t, http.StatusOK)

	d := destination(t, "chat", notify.Config{HTTP: &notify.HTTP{
		URL:     srv.URL + "/hook",
		Headers: map[string]string{"x-api-key": "placeholder-key"},
		Body: `{"text": {{ printf "%s\n%s" .Title .Body | json }}, "scan": {{ .ScanID }},` +
			` "devices": [{{ range $i, $e := .Events }}{{ if $i }}, {{ end }}{{ json $e.Device }}{{ end }}]}`,
	}})
	assert.Equal(t, notify.KindHTTP, d.Kind())
	assert.Equal(t, strings.TrimPrefix(srv.URL, "http://"), d.Host())

	require.NoError(t, d.Send(t.Context(), msg))

	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/hook", got.path)
	assert.Equal(t, "application/json", got.header.Get("Content-Type"))
	assert.Equal(t, "placeholder-key", got.header.Get("X-Api-Key"))
	assert.Empty(t, got.header.Get(notify.HeaderSignature), "only the webhook is signed")
	assert.Equal(t, map[string]any{
		"text": msg.Title + "\n" + msg.Body, "scan": float64(7), "devices": []any{"host-a"},
	}, got.body)
}

// The json function escapes what a device name or a message can hold.
func TestHTTPEscapesValues(t *testing.T) {
	srv, got := server(t, http.StatusOK)

	d := destination(t, "chat", notify.Config{HTTP: &notify.HTTP{
		URL:  srv.URL,
		Body: `{"title": {{ json .Title }}, "message": {{ json .Body }}}`,
	}})

	m := notify.Message{Title: `a "quoted" \ title`, Body: "line one\nline <two>\t&"}
	require.NoError(t, d.Send(t.Context(), m))

	assert.Equal(t, map[string]any{"title": m.Title, "message": m.Body}, got.body)
}

// A Content-Type among the headers replaces JSON, and the body is then not
// checked as JSON.
func TestHTTPContentTypeFromHeaders(t *testing.T) {
	var (
		contentType string
		raw         []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		raw, _ = io.ReadAll(r.Body)
	}))
	t.Cleanup(srv.Close)

	d := destination(t, "plain", notify.Config{HTTP: &notify.HTTP{
		URL:     srv.URL,
		Headers: map[string]string{"content-type": "text/plain; charset=utf-8"},
		Body:    "{{ .Title }}: {{ .Body }}",
	}})

	require.NoError(t, d.Send(t.Context(), msg))
	assert.Equal(t, "text/plain; charset=utf-8", contentType)
	assert.Equal(t, msg.Title+": "+msg.Body, string(raw))
}

// A token in the URL or a header must not reach the log.
func TestHTTPErrorsNameTheHostOnly(t *testing.T) {
	srv, _ := server(t, http.StatusForbidden)
	host := strings.TrimPrefix(srv.URL, "http://")

	d := destination(t, "chat", notify.Config{HTTP: &notify.HTTP{
		URL:     srv.URL + "/botplaceholder-token/sendMessage",
		Headers: map[string]string{"Authorization": "Bearer placeholder-header"},
		Body:    `{"text": {{ json .Body }}}`,
	}})

	err := d.Send(t.Context(), msg)
	require.Error(t, err)
	assert.Equal(t, host+" answered 403 Forbidden", err.Error())
}

func TestHTTPRejectsBadConfig(t *testing.T) {
	const url = "https://hooks.example.com/x"

	for name, h := range map[string]notify.HTTP{
		"without url":         {Body: `{}`},
		"relative url":        {URL: "/x", Body: `{}`},
		"without body":        {URL: url},
		"blank body":          {URL: url, Body: "  \n"},
		"body does not parse": {URL: url, Body: `{"text": {{ json .Title }`},
		"unknown field":       {URL: url, Body: `{"text": {{ json .Nope }}}`},
		"unknown function":    {URL: url, Body: `{"text": {{ shout .Title }}}`},
		"unquoted value":      {URL: url, Body: `{"text": "{{ .Body }}"}`},
		"not json":            {URL: url, Body: `text={{ .Title }}`},
		"bad header name":     {URL: url, Body: `{}`, Headers: map[string]string{"Bad Header": "x"}},
		"bad header value":    {URL: url, Body: `{}`, Headers: map[string]string{"X-Key": "a\nb"}},
		"indexes no events":   {URL: url, Body: `{"text": {{ json (index .Events 0).Device }}}`},
	} {
		_, err := notify.NewDestination("x", notify.Config{HTTP: &h})
		assert.Error(t, err, name)
	}
}

// truncate cuts by character, so a multi-byte name is not split, and leaves a
// string that fits alone.
func TestHTTPTruncate(t *testing.T) {
	srv, got := server(t, http.StatusOK)

	d := destination(t, "chat", notify.Config{HTTP: &notify.HTTP{
		URL:  srv.URL,
		Body: `{"short": {{ .Title | truncate 5 | json }}, "fits": {{ .Body | truncate 50 | json }}}`,
	}})

	require.NoError(t, d.Send(t.Context(), notify.Message{Title: "héllo wörld", Body: "fits"}))
	assert.Equal(t, map[string]any{"short": "héll…", "fits": "fits"}, got.body)
}

// A provider that did not pass Validate sends nothing.
func TestHTTPSendsOnlyAValidatedTemplate(t *testing.T) {
	h := &notify.HTTP{URL: "https://hooks.example.com/x", Body: `{}`}
	require.Error(t, h.Send(t.Context(), msg))
}
