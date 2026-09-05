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
	"github.com/pushkar-anand/jocasta/internal/auth"
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
func NewHandler(
	log *slog.Logger,
	reader *request.Reader,
	store *inventory.Store,
	hw *response.HTMLWriter,
	sm *auth.Session,
	a *auth.Auth,
) *Handler {
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

	hw = hw.WithTemplates(templates)

	staticFS, err := fs.Sub(static, "statics")
	if err != nil {
		panic(err)
	}

	h := &Handler{
		mux:        http.NewServeMux(),
		store:      store,
		htmlWriter: hw,
		reader:     reader,
		log:        log,
	}

	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	h.mux.HandleFunc("GET /setup", hw.Handle(h.setup()))
	h.mux.HandleFunc("POST /setup", hw.Handle(h.setupForm(sm, a)))

	h.mux.HandleFunc("GET /login", hw.Handle(h.login(sm)))
	h.mux.HandleFunc("POST /login", hw.Handle(h.loginForm(sm, a)))
	h.mux.HandleFunc("GET /logout", hw.Handle(h.logout(sm)))

	h.mux.HandleFunc("GET /settings/tokens", hw.Handle(h.tokens(sm, a)))
	h.mux.HandleFunc("POST /settings/tokens", hw.Handle(h.createToken(sm, a)))
	h.mux.HandleFunc("DELETE /settings/tokens/{id}", hw.Handle(h.revokeToken(sm, a)))

	h.mux.HandleFunc("GET /settings/users", hw.Handle(h.users(sm, a)))
	h.mux.HandleFunc("POST /settings/users", hw.Handle(h.createUser(sm, a)))

	// {$} matches only the root itself, so an unknown path reaches the
	// catch-all below and is reported rather than quietly served the overview.
	h.mux.HandleFunc("GET /{$}", hw.Handle(h.overview(sm, a)))
	h.mux.HandleFunc("GET /overview/live", hw.Handle(h.overviewLive()))

	// The literal is the more specific pattern, so it wins over {id}.
	h.mux.HandleFunc("GET /devices", hw.Handle(h.listDevices(sm, a)))
	h.mux.HandleFunc("GET /devices/rows", hw.Handle(h.deviceRows()))
	h.mux.HandleFunc("GET /devices/{id}", hw.Handle(h.device(sm, a)))
	h.mux.HandleFunc("PATCH /devices/{id}", hw.Handle(h.updateDevice()))
	h.mux.HandleFunc("GET /devices/{id}/row", hw.Handle(h.deviceRow()))
	h.mux.HandleFunc("GET /devices/{id}/edit", hw.Handle(h.deviceRowForm()))
	h.mux.HandleFunc("PATCH /devices/{id}/row", hw.Handle(h.updateDeviceRow()))

	h.mux.HandleFunc("GET /networks/{id}", hw.Handle(h.network(sm, a)))
	h.mux.HandleFunc("GET /networks/{id}/rows", hw.Handle(h.networkRows()))

	h.mux.HandleFunc("GET /events", hw.Handle(h.events(sm, a)))
	h.mux.HandleFunc("GET /scans", hw.Handle(h.scans(sm, a)))

	h.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		hw.ErrorPage(w, r, http.StatusNotFound)
	})

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
func lastSweptAt(ctx context.Context, store *inventory.Store) time.Time {
	at, err := store.LastSuccessfulScanAt(ctx, dbtype.ScanDiscovery)
	if err != nil {
		return time.Time{}
	}

	return at
}

// ErrorPageData is the response.WithErrorDataFunc hook the server wires into
// the shared HTMLWriter, keyed by status the same way WithErrorTemplates is --
// each case supplies whatever its own template needs.
func ErrorPageData(_ *http.Request, _ error, status int) map[string]any {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		// A request the sender can fix and resend: it did not parse, was too
		// large, or failed validation. Rendered from layout/head like the 404
		// case below, since almost every form that reaches it is behind the
		// signed-in shell; setup and sign-in guard their own inputs in the
		// markup, so a crafted request is the only way they land here.
		return map[string]any{
			"Title":   "Bad request",
			"Section": "",
			"Crumb":   nil,
			"Live":    false,
			"Note":    "",
		}
	case http.StatusUnauthorized:
		// The sign-in page's own fields -- see loginData -- not the signed-in
		// shell's, since TemplateLogin renders standalone like login itself does.
		return map[string]any{
			"Title": "Sign in",
			"Error": "Incorrect username or password.",
		}
	case http.StatusConflict:
		// The setup page's own fields, the same reason the 401 case above uses
		// loginData's rather than view's: TemplateSetup renders standalone too.
		return map[string]any{
			"Title": "Set up admin account",
			"Error": "Setup has already been completed. Sign in instead.",
		}
	case http.StatusForbidden:
		// Forbidden renders inside the signed-in shell -- the visitor reaching
		// it is signed in, just not as an admin -- so it needs view's fields
		// the same way the 404 case below does.
		return map[string]any{
			"Title":   "Forbidden",
			"Section": "",
			"Crumb":   nil,
			"Live":    false,
			"Note":    "",
		}
	default:
		// The 404 page is built from layout/head and layout/foot like every
		// other page, so it needs the same view fields; every one beyond Title
		// is left at its zero value.
		return map[string]any{
			"Title":   "Not found",
			"Section": "",
			"Crumb":   nil,
			"Live":    false,
			"Note":    "",
		}
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
