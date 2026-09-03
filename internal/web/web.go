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
	"strconv"
	"time"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
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

// Handler serves the HTML UI over the same store the JSON API reads.
type Handler struct {
	mux      *http.ServeMux
	store    *inventory.Store
	renderer *Renderer
	reader   *request.Reader
	log      *slog.Logger
}

// ServeHTTP routes a request to the page or fragment handler that matches it.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// NewHandler builds the web routes, parsing the embedded templates and
// mounting the static assets.
func NewHandler(log *slog.Logger, reader *request.Reader, store *inventory.Store) *Handler {
	// A template that does not parse is a broken build, not a runtime
	// condition: every one of them is compiled into the binary.
	templates := template.Must(template.New("").
		Funcs(funcs(time.Now)).
		ParseFS(templatesFS, "templates/pages/*.html.tmpl", "templates/partials/*.html.tmpl"))

	staticFS, err := fs.Sub(static, "statics")
	if err != nil {
		panic(err)
	}

	h := &Handler{
		mux:      http.NewServeMux(),
		store:    store,
		renderer: NewRenderer(templates, log),
		reader:   reader,
		log:      log,
	}

	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	// {$} matches only the root itself, so an unknown path reaches the
	// catch-all below and is reported rather than quietly served the overview.
	h.mux.HandleFunc("GET /{$}", h.overview)
	h.mux.HandleFunc("GET /overview/live", h.overviewLive)

	// The literal is the more specific pattern, so it wins over {id}.
	h.mux.HandleFunc("GET /devices", h.devices)
	h.mux.HandleFunc("GET /devices/rows", h.deviceRows)
	h.mux.HandleFunc("GET /devices/{id}", h.device)
	h.mux.HandleFunc("PATCH /devices/{id}", h.updateDevice)
	h.mux.HandleFunc("GET /devices/{id}/row", h.deviceRow)
	h.mux.HandleFunc("GET /devices/{id}/edit", h.deviceRowEdit)
	h.mux.HandleFunc("PATCH /devices/{id}/row", h.updateDeviceRow)

	h.mux.HandleFunc("GET /networks/{id}", h.network)

	h.mux.HandleFunc("GET /events", h.events)
	h.mux.HandleFunc("GET /scans", h.scans)

	h.mux.HandleFunc("GET /", h.notFound)

	return h
}

// nav is one entry of the sidebar navigation. The entries live in Go so a page
// that does not exist yet cannot be linked to from the layout, and so a section
// cannot be added without the glyph that names it in the rail.
type nav struct {
	Label   string
	Href    string
	Current bool

	// Icon is the glyph's paths, on a 24px grid and stroked in currentColor.
	// It is markup this package owns rather than anything a request supplies,
	// which is what makes carrying it as HTML safe.
	Icon template.HTML
}

var sections = []nav{
	{
		Label: "Overview", Href: "/",
		Icon: `<rect x="3" y="3" width="8" height="8" rx="2"/><rect x="13" y="3" width="8" height="8" rx="2"/>` +
			`<rect x="3" y="13" width="8" height="8" rx="2"/><rect x="13" y="13" width="8" height="8" rx="2"/>`,
	},
	{
		Label: "Devices", Href: "/devices",
		Icon: `<rect x="3" y="4" width="18" height="6" rx="1.5"/><rect x="3" y="14" width="18" height="6" rx="1.5"/>` +
			`<circle cx="7" cy="7" r="0.7"/><circle cx="7" cy="17" r="0.7"/>`,
	},
	{
		Label: "Events", Href: "/events",
		Icon: `<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>`,
	},
	{
		Label: "Scans", Href: "/scans",
		Icon: `<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="4.2"/><circle cx="12" cy="12" r="1"/>` +
			`<path d="M12 2.3v2.2M21.7 12h-2.2M12 21.7v-2.2M2.3 12h2.2"/>`,
	},
}

// crumb is the way back out of a page that is about one thing. Only such a page
// sets one; every other page is reached from the rail, which is already there.
type crumb struct {
	Label string
	Href  string
}

// view is what the layout needs from every page.
type view struct {
	Title   string
	Section string

	// Crumb is the way back, shown before the title. Nil leaves it out.
	Crumb *crumb

	// Live marks a page that refreshes itself, which is the only thing the
	// indicator in the topbar claims.
	Live bool

	// Note is the ambient line at the foot of the rail. Empty leaves it out.
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
	Stats    *inventory.Stats
	Scan     *inventory.Scan
	Networks []*inventory.Network
	Events   []*inventory.Event
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
// the page polls for. It answers with the body alone: the #live wrapper that
// drives the poll stays on the page across every refresh.
func (h *Handler) overviewLive(w http.ResponseWriter, r *http.Request) {
	data, err := h.overviewData(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.renderer.Render(w, r, "partial/live-body", data)
}

func (h *Handler) overviewData(r *http.Request) (*overviewData, error) {
	ctx := r.Context()

	stats, err := h.store.Stats(ctx)
	if err != nil {
		return nil, err
	}

	networks, err := h.store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	// The overview shows the top of the log and never walks it, so it takes
	// the first page and drops the cursor that would continue it.
	activity, err := h.store.ListEvents(ctx, inventory.Page{Limit: activityLimit})
	if err != nil {
		return nil, err
	}

	data := &overviewData{
		view:     view{Title: "Overview", Section: "Overview", Live: true},
		Stats:    stats,
		Networks: networks,
		Events:   activity.Events,
	}

	// A first run has no sweep behind it, which is a state to render rather
	// than a failure to report.
	if scan, err := h.store.LatestScan(ctx); err == nil {
		data.Scan = scan
		data.Note = sweepNote(scan)
	}

	return data, nil
}

// sweepNote is the ambient line every page carries at the foot of its rail. The error
// is returned rather than swallowed so a caller can tell "no sweep yet" from a
// read that failed, and leave the line out either way.
func (h *Handler) sweepNote(ctx context.Context) (string, error) {
	scan, err := h.store.LatestScan(ctx)
	if err != nil {
		return "", err
	}

	return sweepNote(scan), nil
}

func sweepNote(scan *inventory.Scan) string {
	// The rail labels the line "Last sweep", so the verb would be said twice.
	return ago(time.Now(), scan.StartedAt)
}

// lastSweptAt is when a device sweep last finished with something to show for
// it, zero before the first. Shown on the device page beside the device's own
// last_seen: together they separate a device that has left from sweeps that
// have stopped.
func (h *Handler) lastSweptAt(ctx context.Context) time.Time {
	at, err := h.store.LastSuccessfulScanAt(ctx, dbtype.ScanDiscovery)
	if err != nil {
		return time.Time{}
	}

	return at
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

// Renderer writes html/template pages and fragments to an [http.ResponseWriter].
type Renderer struct {
	templates *template.Template
	log       *slog.Logger
}

// NewRenderer returns a Renderer over an already-parsed template set.
func NewRenderer(templates *template.Template, log *slog.Logger) *Renderer {
	return &Renderer{
		templates: templates,
		log:       log,
	}
}

// Render writes the named template. Pages and fragments go through here alike:
// with htmx there is no third thing a fragment needs.
func (rr *Renderer) Render(w http.ResponseWriter, r *http.Request, templateName string, templateData any) {
	rr.RenderStatus(w, r, http.StatusOK, templateName, templateData)
}

// RenderStatus writes the named template under a given status code.
func (rr *Renderer) RenderStatus(
	w http.ResponseWriter,
	r *http.Request,
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
		rr.log.ErrorContext(r.Context(), "error rendering template",
			logger.Err(err), slog.String("template", templateName))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	_, _ = buf.WriteTo(w)
}

// HTML wraps Render so a template can be served directly as an [http.HandlerFunc].
func (rr *Renderer) HTML(tmpl string, data any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rr.Render(w, r, tmpl, data)
	}
}

// pathID reads the {id} the route captured. Every route carrying one admits any
// segment, so this is where a value that is not a positive id is turned away;
// the caller decides how.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}

	return id, true
}
