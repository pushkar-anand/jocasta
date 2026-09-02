package web

import (
	"context"
	"database/sql"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Addresses come from RFC 5737 and hardware addresses from RFC 7042, both
// reserved for documentation, so nothing here names a real device.
const (
	prefix = "192.0.2.0/24"

	macA = "00:00:5e:00:53:01"
	macB = "00:00:5e:00:53:02"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testStore opens an inventory over a migrated database scoped to the test.
func testStore(t *testing.T) *inventory.Store {
	t.Helper()

	store, _ := testStoreWithConn(t)

	return store
}

// testStoreWithConn also hands back the connection, for a test that has to set
// up a state the store's own writes cannot reach.
func testStoreWithConn(t *testing.T) (*inventory.Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Conn.Close() })

	return inventory.New(conn.Conn, testLogger()), conn.Conn
}

// testReader builds the request reader the server wires in.
func testReader(t *testing.T) *request.Reader {
	t.Helper()

	v, err := validator.New()
	require.NoError(t, err)

	return request.NewReader(testLogger(), v)
}

// empty returns a handler over an inventory nothing has swept into.
func empty(t *testing.T) *Handler {
	t.Helper()

	return NewHandler(testLogger(), testReader(t), testStore(t))
}

// seeded returns a handler over an inventory holding two swept devices.
func seeded(t *testing.T) *Handler {
	t.Helper()

	store := testStore(t)

	swept := []scanner.Host{
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	}

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), swept)
	require.NoError(t, err)

	return NewHandler(testLogger(), testReader(t), store)
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))

	return rec
}

func TestOverviewRendersTheInventory(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))

	body := rec.Body.String()

	assert.Contains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, "jocasta")

	// The ledger is the page's one graphic, and its proportion has to come out
	// as a number: html/template replaces a value it cannot vouch for in an
	// attribute with ZgotmplZ rather than failing.
	assert.Contains(t, body, `class="ledger"`)
	assert.Contains(t, body, `width="100"`)
	assert.NotContains(t, body, "ZgotmplZ")

	// Both swept devices are counted, and the activity log shows what the
	// sweep recorded.
	assert.Contains(t, body, "Seen recently")
	assert.Contains(t, body, "printer.local")
	assert.Contains(t, body, "discovered")

	// The sweep that produced all this is named.
	assert.Contains(t, body, "test-sweep")
	assert.Contains(t, body, prefix)
}

// An empty inventory is a state to explain, not a blank page: nothing here
// probes on its own, so the operator has to be told what to run.
func TestOverviewWithoutAnySweepInvitesOne(t *testing.T) {
	t.Parallel()

	rec := get(t, empty(t), "/")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "No devices yet")
	assert.Contains(t, body, "jocasta scan 192.168.1.0/24 --save")

	// The invitation replaces the live block rather than sitting under an empty
	// one: a ledger of nothing says less than the instruction does.
	assert.NotContains(t, body, `id="live"`)
	assert.NotContains(t, body, "Seen recently")
}

// The fragment endpoint and the page differ only in which template they name,
// so the fragment must come back on its own rather than wrapped in a document.
func TestOverviewLiveServesAFragment(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/overview/live")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))

	body := rec.Body.String()

	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.NotContains(t, body, "<body>")
	assert.Contains(t, body, `id="live"`)
	assert.Contains(t, body, "Seen recently")

	// The fragment carries the attributes that make it refresh itself, so the
	// swapped-in copy keeps polling.
	assert.Contains(t, body, `hx-get="/overview/live"`)
	assert.Contains(t, body, `hx-trigger="every 30s"`)
}

// The root pattern ends in {$}, so an unknown path is reported rather than
// quietly served the overview.
func TestUnknownPathIsNotFound(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/nope")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "There is nothing at this address")
}

func TestStaticFiles(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	tests := []struct {
		target      string
		contentType string
	}{
		{"/static/style.css", "text/css"},
		{"/static/js/htmx.min.js", "javascript"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			t.Parallel()

			rec := get(t, h, tc.target)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Header().Get("Content-Type"), tc.contentType)
			assert.NotEmpty(t, rec.Body.Bytes())
		})
	}
}

func TestMissingStaticFile(t *testing.T) {
	t.Parallel()

	assert.Equal(t, http.StatusNotFound, get(t, seeded(t), "/static/nope.css").Code)
}

// Every template file defines exactly one name, and those names are what the
// handlers ask for. A rename that misses one would only show up at runtime.
func TestEveryNamedTemplateExists(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, name := range []string{
		"page/dashboard", "page/notfound",
		"partial/live", "partial/activity",
		"layout/head", "layout/foot",
	} {
		assert.NotNil(t, h.renderer.templates.Lookup(name), "template %q should be parsed", name)
	}
}

func TestRendererRender(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(template.New("greet.tmpl").Parse(`Hello {{ .Name }}!`))
	r := NewRenderer(tmpl, testLogger())

	rec := httptest.NewRecorder()
	r.Render(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), "greet.tmpl", map[string]string{"Name": "Ada"})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "Hello Ada!", rec.Body.String())
}

func TestRendererRenderStatus(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(template.New("greet.tmpl").Parse(`Gone`))
	r := NewRenderer(tmpl, testLogger())

	rec := httptest.NewRecorder()
	r.RenderStatus(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), http.StatusGone, "greet.tmpl", nil)

	require.Equal(t, http.StatusGone, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "Gone", rec.Body.String())
}

// TestRendererEscapesData guards the reason the handler uses html/template
// rather than text/template.
func TestRendererEscapesData(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(template.New("greet.tmpl").Parse(`Hello {{ .Name }}!`))
	r := NewRenderer(tmpl, testLogger())

	rec := httptest.NewRecorder()
	r.Render(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), "greet.tmpl", map[string]string{
		"Name": `<script>alert(1)</script>`,
	})

	assert.NotContains(t, rec.Body.String(), "<script>")
	assert.Contains(t, rec.Body.String(), "&lt;script&gt;")
}

func TestRendererUnknownTemplate(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(template.New("greet.tmpl").Parse(`Hello!`))
	r := NewRenderer(tmpl, testLogger())

	rec := httptest.NewRecorder()
	r.Render(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), "missing.tmpl", nil)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRendererExecutionErrorPreventsPartialResponse(t *testing.T) {
	t.Parallel()

	// A template that fails halfway through execution
	tmpl := template.Must(template.New("bad.tmpl").Parse(`Good Start... {{ .MissingField }}`))
	r := NewRenderer(tmpl, testLogger())

	rec := httptest.NewRecorder()
	r.Render(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), "bad.tmpl", struct{}{})

	// Should respond with 500
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	// Should not have any part of the template rendered
	assert.NotContains(t, rec.Body.String(), "Good Start...")
	// Should contain standard http.Error body
	assert.Contains(t, rec.Body.String(), "Internal Server Error")
}

func TestRendererHTML(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(template.New("greet.tmpl").Parse(`Hello {{ . }}!`))
	handler := NewRenderer(tmpl, testLogger()).HTML("greet.tmpl", "Ada")

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Hello Ada!", rec.Body.String())
}

func TestNavMarksTheCurrentSection(t *testing.T) {
	t.Parallel()

	entries := view{Section: "Overview"}.Nav()
	require.NotEmpty(t, entries)

	current := 0

	for _, e := range entries {
		if e.Current {
			current++

			assert.Equal(t, "Overview", e.Label)
		}
	}

	assert.Equal(t, 1, current, "exactly one entry should be current")

	// A section that names nothing in the nav marks nothing, rather than
	// marking the first entry.
	for _, e := range (view{Section: "Nowhere"}).Nav() {
		assert.False(t, e.Current)
	}
}

// host builds a swept host the way a sweep does. A malformed argument is a
// broken test.
func host(ip, mac, hostname string) scanner.Host {
	h, err := hosts.BuildHost(context.Background(), hosts.HostInput{IP: ip, MAC: mac, Hostname: hostname})
	if err != nil {
		panic(err)
	}

	return scanner.Host{Host: h}
}
