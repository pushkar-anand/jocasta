package web

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/pushkar-anand/build-with-go/logger"
)

//go:embed statics/*
var static embed.FS

//go:embed templates/*.html.tmpl
var templatesFS embed.FS

type Handler struct {
	*http.ServeMux
	renderer *Renderer
}

func NewHandler(logger *slog.Logger) *Handler {
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	err := rr.templates.ExecuteTemplate(w, templateName, templateData)
	if err != nil {
		rr.logger.ErrorContext(request.Context(), "error rendering template", logger.Err(err), slog.String("template", templateName))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (rr *Renderer) HTML(tmpl string, data any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rr.Render(w, r, tmpl, data)
	}
}
