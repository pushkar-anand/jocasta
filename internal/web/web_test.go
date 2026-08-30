package web

import (
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHandlerServesIndex(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NewHandler(testLogger()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "Hello World!")
}

func TestHandlerServesStaticFiles(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NewHandler(testLogger()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/style.css", nil))

	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "text/css")
}

func TestHandlerMissingStaticFile(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NewHandler(testLogger()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/nope.css", nil))

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

func TestRendererHTML(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(template.New("greet.tmpl").Parse(`Hello {{ . }}!`))
	handler := NewRenderer(tmpl, testLogger()).HTML("greet.tmpl", "Ada")

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Hello Ada!", rec.Body.String())
}
