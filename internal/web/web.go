// Package web serves the inventory as HTML. Interactivity comes from htmx
// attributes on server-rendered markup, so a fragment endpoint and a page
// endpoint differ only in which template they name.
package web

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

//go:embed statics/*
var static embed.FS

// Pages and partials are parsed into one template set. Every file carries a
// single namespaced define -- page/dashboard, partial/live, layout/head -- which
// is what makes the flat set safe: templates parsed together share one
// namespace, so a name repeated across files would have the last one parsed
// silently replace the rest.
//
//go:embed templates/pages/*.html.tmpl templates/partials/*.html.tmpl
var templatesFS embed.FS

// activityLimit is how much of the change log the overview shows. The full log
// has its own page.
const activityLimit = 12

type Handler struct {
	*http.ServeMux
	store    *inventory.Store
	renderer *Renderer
	reader   *request.Reader
	log      *slog.Logger
}

func NewHandler(log *slog.Logger, reader *request.Reader, store *inventory.Store) *Handler {
	templates, err := template.New("").
		Funcs(funcs(time.Now)).
		ParseFS(templatesFS, "templates/pages/*.html.tmpl", "templates/partials/*.html.tmpl")
	if err != nil {
		// A template that does not parse is a broken build, not a runtime
		// condition: every one of them is compiled into the binary.
		panic(err)
	}

	staticFS, err := fs.Sub(static, "statics")
	if err != nil {
		panic(err)
	}

	h := &Handler{
		ServeMux: http.NewServeMux(),
		store:    store,
		renderer: NewRenderer(templates, log),
		reader:   reader,
		log:      log,
	}

	h.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	// {$} matches only the root itself, so an unknown path reaches the
	// catch-all below and is reported rather than quietly served the overview.
	h.HandleFunc("GET /{$}", h.overview)
	h.HandleFunc("GET /overview/live", h.overviewLive)

	// The literal is the more specific pattern, so it wins over {id}.
	h.HandleFunc("GET /devices", h.devices)
	h.HandleFunc("GET /devices/rows", h.deviceRows)
	h.HandleFunc("GET /devices/{id}", h.device)
	h.HandleFunc("PATCH /devices/{id}", h.updateDevice)
	h.HandleFunc("GET /devices/{id}/row", h.deviceRow)
	h.HandleFunc("GET /devices/{id}/edit", h.deviceRowEdit)
	h.HandleFunc("PATCH /devices/{id}/row", h.updateDeviceRow)

	h.HandleFunc("GET /events", h.events)
	h.HandleFunc("GET /scans", h.scans)

	h.HandleFunc("GET /", h.notFound)

	return h
}

// nav is one entry of the masthead navigation. The entries live in Go so a page
// that does not exist yet cannot be linked to from the layout.
type nav struct {
	Label   string
	Href    string
	Current bool
}

var sections = []nav{
	{Label: "Overview", Href: "/"},
	{Label: "Devices", Href: "/devices"},
	{Label: "Activity", Href: "/events"},
	{Label: "Sweeps", Href: "/scans"},
}

// view is what the layout needs from every page.
type view struct {
	Title   string
	Section string

	// Note is the ambient line at the end of the masthead. Empty leaves it out.
	Note string
}

// Nav returns the masthead entries with the current one marked.
func (v view) Nav() []nav {
	entries := make([]nav, 0, len(sections))

	for _, s := range sections {
		s.Current = s.Label == v.Section
		entries = append(entries, s)
	}

	return entries
}

// overviewData is the whole overview, and also every part of it that refreshes
// on its own, since the fragment is rendered from the same value.
type overviewData struct {
	view
	Stats  inventory.Stats
	Scan   *inventory.Scan
	Events []inventory.Event
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	data, err := h.overviewData(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.renderer.Render(w, r, "page/dashboard", data)
}

// overviewLive serves the part of the overview that goes stale, which is what
// the page polls for.
func (h *Handler) overviewLive(w http.ResponseWriter, r *http.Request) {
	data, err := h.overviewData(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.renderer.Render(w, r, "partial/live", data)
}

func (h *Handler) overviewData(r *http.Request) (overviewData, error) {
	ctx := r.Context()

	stats, err := h.store.Stats(ctx)
	if err != nil {
		return overviewData{}, err
	}

	// The overview shows the top of the log and never walks it, so it takes
	// the first page and drops the cursor that would continue it.
	activity, err := h.store.ListEvents(ctx, inventory.Page{Limit: activityLimit})
	if err != nil {
		return overviewData{}, err
	}

	data := overviewData{
		view:   view{Title: "Overview", Section: "Overview"},
		Stats:  stats,
		Events: activity.Events,
	}

	// A first run has no sweep behind it, which is a state to render rather
	// than a failure to report.
	if scan, err := h.store.LatestScan(ctx); err == nil {
		data.Scan = &scan
		data.Note = sweepNote(scan)
	}

	return data, nil
}

// sweepNote is the ambient line every page carries in its masthead. The error
// is returned rather than swallowed so a caller can tell "no sweep yet" from a
// read that failed, and leave the line out either way.
func (h *Handler) sweepNote(ctx context.Context) (string, error) {
	scan, err := h.store.LatestScan(ctx)
	if err != nil {
		return "", err
	}

	return sweepNote(scan), nil
}

func sweepNote(scan inventory.Scan) string {
	return "swept " + ago(time.Now(), scan.StartedAt)
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	h.renderer.RenderStatus(w, r, http.StatusNotFound, "page/notfound", view{Title: "Not found"})
}

// fail reports a read that did not work. There is nothing the visitor can do
// about it, so the page says so plainly and the detail goes to the log.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "failed to read the inventory",
		logger.Err(err), slog.String("path", r.URL.Path))

	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
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

// Render writes the named template. Pages and fragments go through here alike:
// with htmx there is no third thing a fragment needs.
func (rr *Renderer) Render(w http.ResponseWriter, request *http.Request, templateName string, templateData any) {
	rr.RenderStatus(w, request, http.StatusOK, templateName, templateData)
}

// RenderStatus writes the named template under a given status code.
func (rr *Renderer) RenderStatus(
	w http.ResponseWriter,
	request *http.Request,
	status int,
	templateName string,
	templateData any,
) {
	// Rendered to a buffer first so a template that fails halfway through
	// cannot leave half a page followed by an error, and so the status is not
	// already written when it does.
	var buf bytes.Buffer

	err := rr.templates.ExecuteTemplate(&buf, templateName, templateData)
	if err != nil {
		rr.logger.ErrorContext(request.Context(), "error rendering template", logger.Err(err), slog.String("template", templateName))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	_, _ = buf.WriteTo(w)
}

func (rr *Renderer) HTML(tmpl string, data any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rr.Render(w, r, tmpl, data)
	}
}
