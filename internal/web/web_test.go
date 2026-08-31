package web

import (
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testDB opens a migrated database in a directory scoped to the test. The
// handler does not read from it yet, but it is wired in the way main wires it
// so the tests break when that changes.
func testDB(t *testing.T) *db.DB {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Conn.Close() })

	return conn
}

func TestHandlerServesIndex(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NewHandler(testLogger(), testDB(t)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", res.Header.Get("X-Frame-Options"))
	assert.Equal(t, "default-src 'self'", res.Header.Get("Content-Security-Policy"))
	assert.Contains(t, rec.Body.String(), "Hello World!")
}

func TestHandlerServesStaticFiles(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NewHandler(testLogger(), testDB(t)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/style.css", nil))

	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "text/css")
}

func TestHandlerMissingStaticFile(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NewHandler(testLogger(), testDB(t)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/nope.css", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRendererRender(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(template.New("greet.tmpl").Parse(`Hello {{ .Name }}!`))
	r := NewRenderer(tmpl, testLogger())

	rec := httptest.NewRecorder()
	r.Render(rec, httptest.NewRequest(http.MethodGet, "/", nil), "greet.tmpl", map[string]string{"Name": "Ada"})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "default-src 'self'", rec.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "Hello Ada!", rec.Body.String())
}

// TestRendererEscapesData guards the reason the handler uses html/template
// rather than text/template.
func TestRendererEscapesData(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(template.New("greet.tmpl").Parse(`Hello {{ .Name }}!`))
	r := NewRenderer(tmpl, testLogger())

	rec := httptest.NewRecorder()
	r.Render(rec, httptest.NewRequest(http.MethodGet, "/", nil), "greet.tmpl", map[string]string{
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
	r.Render(rec, httptest.NewRequest(http.MethodGet, "/", nil), "missing.tmpl", nil)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRendererExecutionErrorPreventsPartialResponse(t *testing.T) {
	t.Parallel()

	// A template that fails halfway through execution
	tmpl := template.Must(template.New("bad.tmpl").Parse(`Good Start... {{ .MissingField }}`))
	r := NewRenderer(tmpl, testLogger())

	rec := httptest.NewRecorder()
	r.Render(rec, httptest.NewRequest(http.MethodGet, "/", nil), "bad.tmpl", struct{}{})

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
	handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Hello Ada!", rec.Body.String())
}
