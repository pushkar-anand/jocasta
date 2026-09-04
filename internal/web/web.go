// Package web serves the inventory as HTML. Interactivity comes from htmx
// attributes on server-rendered markup, so a fragment endpoint and a page
// endpoint differ only in which template they name.
package web

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/response"
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
	mux        *http.ServeMux
	store      *inventory.Store
	htmlWriter *response.HTMLWriter
	reader     *request.Reader
	log        *slog.Logger
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
	templates := template.Must(
		template.New("").
			Funcs(funcs(time.Now)).
			ParseFS(
				templatesFS,
				"templates/pages/*.html.tmpl",
				"templates/partials/*.html.tmpl"),
	)

	staticFS, err := fs.Sub(static, "statics")
	if err != nil {
		panic(err)
	}

	hw := response.NewHTMLWriter(log, templates)

	h := &Handler{
		mux:        http.NewServeMux(),
		store:      store,
		htmlWriter: hw,
		reader:     reader,
		log:        log,
	}

	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	// {$} matches only the root itself, so an unknown path reaches the
	// catch-all below and is reported rather than quietly served the overview.
	h.mux.HandleFunc("GET /{$}", h.overview())
	h.mux.HandleFunc("GET /overview/live", h.overviewLive())

	// The literal is the more specific pattern, so it wins over {id}.
	h.mux.HandleFunc("GET /devices", h.devices)
	h.mux.HandleFunc("GET /devices/rows", h.deviceRows)
	h.mux.HandleFunc("GET /devices/{id}", h.device)
	h.mux.HandleFunc("PATCH /devices/{id}", h.updateDevice)
	h.mux.HandleFunc("GET /devices/{id}/row", h.deviceRow)
	h.mux.HandleFunc("GET /devices/{id}/edit", h.deviceRowEdit)
	h.mux.HandleFunc("PATCH /devices/{id}/row", h.updateDeviceRow)

	h.mux.HandleFunc("GET /networks/{id}", h.network)
	h.mux.HandleFunc("GET /networks/{id}/rows", h.networkRows)

	h.mux.HandleFunc("GET /events", h.events)
	h.mux.HandleFunc("GET /scans", h.scans)

	h.mux.HandleFunc("GET /", h.notFound())

	return h
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

func (h *Handler) notFound() http.HandlerFunc {
	v := view{Title: "Not found"}

	return func(w http.ResponseWriter, r *http.Request) {
		h.htmlWriter.NotFound(w, r, templateNotFound, v)
	}
}

// fail reports a read that did not work. There is nothing the visitor can do
// about it, so the page says so plainly and the detail goes to the log.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "failed to read the inventory",
		logger.Err(err), slog.String("path", r.URL.Path))

	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
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
