package web

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db"
)

//go:embed statics/*
var static embed.FS

//go:embed templates/*.html.tmpl
var templatesFS embed.FS

type Handler struct {
	*http.ServeMux
	renderer *Renderer
	reader   *request.Reader
}

func NewHandler(logger *slog.Logger, conn *db.DB, reader *request.Reader) *Handler {
	mux := http.NewServeMux()

	templates, err := template.ParseFS(templatesFS, "templates/*.html.tmpl")
	if err != nil {
		panic(err)
	}

	staticFS, err := fs.Sub(static, "statics")
	if err != nil {
		panic(err)
	}

	r := NewRenderer(templates, logger)

	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	mux.HandleFunc("/", r.HTML("index.html.tmpl", nil))

	return &Handler{
		ServeMux: mux,
		renderer: r,
		reader:   reader,
	}
}

type Renderer struct {
	templates *template.Template
	logger    *slog.Logger
}

// NewRenderer creates a new template Renderer
func NewRenderer(templates *template.Template, logger *slog.Logger) *Renderer {
	return &Renderer{
		templates: templates,
		logger:    logger,
	}
}

func (rr *Renderer) Render(w http.ResponseWriter, request *http.Request, templateName string, templateData any) {
	var buf bytes.Buffer
	err := rr.templates.ExecuteTemplate(&buf, templateName, templateData)
	if err != nil {
		rr.logger.ErrorContext(request.Context(), "error rendering template", logger.Err(err), slog.String("template", templateName))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'")

	buf.WriteTo(w)
}

func (rr *Renderer) HTML(tmpl string, data any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rr.Render(w, r, tmpl, data)
	}
}
