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
	"net/url"
	"strconv"
	"time"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/notify"
)

//go:embed statics/*
var static embed.FS

// Pages and partials are parsed into one template set. Every file carries a
// single namespaced define (page/dashboard, partial/live, layout/head), which
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

	// recent is what the traffic recorder saw lately; nil when no traffic
	// source is configured.
	recent RecentTraffic

	// homeCountry is the configured country the world map draws from.
	homeCountry string

	// notifier sends changes to the configured destinations; nil when none
	// is enabled.
	notifier *notify.Notifier
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
	opts ...Option,
) *Handler {
	// A template that does not parse is a broken build: every one of them
	// is compiled into the binary.
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

	for _, o := range opts {
		o(h)
	}

	// allow wraps a handler so only an account of at least role want reaches
	// it; anyone else gets the forbidden page.
	allow := func(want dbtype.UserRole) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !sm.CurrentRole(r.Context()).AtLeast(want) {
					hw.ErrorPage(w, r, http.StatusForbidden)
					return
				}

				next.ServeHTTP(w, r)
			})
		}
	}

	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	h.mux.HandleFunc("GET /setup", hw.Handle(h.setup()))
	h.mux.HandleFunc("POST /setup", hw.Handle(h.setupForm(sm, a)))

	h.mux.HandleFunc("GET /login", hw.Handle(h.login(sm)))
	h.mux.HandleFunc("POST /login", hw.Handle(h.loginForm(sm, a)))

	h.mux.HandleFunc("GET /login/totp", hw.Handle(h.loginTOTP(sm)))
	h.mux.HandleFunc("POST /login/totp", hw.Handle(h.loginTOTPForm(sm, a)))

	// Signing out changes state, so it is a POST the sameOrigin guard covers.
	// A link could be spent by another page.
	h.mux.HandleFunc("POST /logout", hw.Handle(h.logout(sm)))

	h.mux.HandleFunc("GET /settings/security", hw.Handle(h.security(sm, a)))
	h.mux.HandleFunc("POST /settings/security/totp/enroll", hw.Handle(h.securityEnroll(sm, a)))
	h.mux.HandleFunc("GET /settings/security/totp-qr.png", hw.Handle(h.totpQR(sm, a)))
	h.mux.HandleFunc("POST /settings/security/totp/confirm", hw.Handle(h.securityConfirm(sm, a)))
	h.mux.HandleFunc("POST /settings/security/totp/disable", hw.Handle(h.securityDisable(sm, a)))
	h.mux.HandleFunc("POST /settings/security/recovery-codes/regenerate", hw.Handle(h.securityRegenerateRecoveryCodes(sm, a)))

	h.mux.HandleFunc("GET /settings/tokens", hw.Handle(h.tokens(sm, a)))

	h.mux.HandleFunc("POST /settings/tokens", hw.Handle(h.createToken(sm, a)))
	h.mux.HandleFunc("DELETE /settings/tokens/{id}", hw.Handle(h.revokeToken(sm, a)))

	h.mux.Handle("GET /settings/users", allow(dbtype.RoleAdmin)(hw.Handle(h.users(sm, a))))
	h.mux.Handle("POST /settings/users", allow(dbtype.RoleAdmin)(hw.Handle(h.createUser(sm, a))))

	h.mux.Handle("GET /settings/notifications", allow(dbtype.RoleAdmin)(hw.Handle(h.notifications(sm))))
	h.mux.Handle("POST /settings/notifications/{name}", allow(dbtype.RoleAdmin)(hw.Handle(h.saveNotifications(sm))))
	h.mux.Handle("POST /settings/notifications/{name}/test", allow(dbtype.RoleAdmin)(hw.Handle(h.testNotification(sm))))

	// {$} matches only the root itself, so an unknown path reaches the
	// catch-all below and is reported as not found.
	h.mux.HandleFunc("GET /{$}", hw.Handle(h.overview(sm)))
	h.mux.HandleFunc("GET /overview/live", hw.Handle(h.overviewLive()))

	// The literal is the more specific pattern, so it wins over {id}.
	h.mux.HandleFunc("GET /devices", hw.Handle(h.listDevices(sm, false)))
	h.mux.HandleFunc("GET /devices/rows", hw.Handle(h.listDevices(sm, true)))
	h.mux.HandleFunc("GET /devices/{id}", hw.Handle(h.device(sm)))
	h.mux.Handle("PATCH /devices/{id}", allow(dbtype.RoleReadWrite)(hw.Handle(h.updateDevice(sm))))
	h.mux.HandleFunc("GET /devices/{id}/row", hw.Handle(h.deviceRow(sm)))
	h.mux.HandleFunc("GET /devices/{id}/traffic", hw.Handle(h.deviceTraffic()))
	h.mux.Handle("GET /devices/{id}/edit", allow(dbtype.RoleReadWrite)(hw.Handle(h.deviceRowForm())))
	h.mux.Handle("PATCH /devices/{id}/row", allow(dbtype.RoleReadWrite)(hw.Handle(h.updateDeviceRow(sm))))

	h.mux.HandleFunc("GET /networks/{id}", hw.Handle(h.network(sm, false)))
	h.mux.HandleFunc("GET /networks/{id}/rows", hw.Handle(h.network(sm, true)))

	h.mux.HandleFunc("GET /traffic", hw.Handle(h.traffic(sm)))
	h.mux.HandleFunc("GET /map", hw.Handle(h.networkMap(sm)))
	h.mux.HandleFunc("GET /map/live", hw.Handle(h.networkMapLive()))
	h.mux.HandleFunc("GET /events", hw.Handle(h.events(sm)))
	h.mux.HandleFunc("GET /scans", hw.Handle(h.scans(sm)))

	h.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		hw.ErrorPage(w, r, http.StatusNotFound)
	})

	return h
}

// latestSweepNote is the ambient line every page carries at the foot of its
// rail. It is empty when there is no sweep yet or the read failed, and the
// page leaves the line out either way.
func latestSweepNote(ctx context.Context, store *inventory.Store) string {
	scan, err := store.LatestScan(ctx)
	if err != nil {
		return ""
	}

	return sweepNote(scan)
}

func sweepNote(scan *inventory.Scan) string {
	// The rail labels the line "Last sweep", so the verb would be said twice.
	note := ago(time.Now(), scan.StartedAt)

	// A collector that still records scan rows while failing every one of them
	// would otherwise keep this line reading as healthy.
	if scan.Status == dbtype.StatusFailed {
		note += " · failed"
	}

	return note
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

// portScanConfigured reports whether any port scan has ever finished. It is an
// instance-wide signal, enough for the Ports section to say "port scanning is
// not set up" where it would otherwise imply a device was scanned and found
// closed.
func portScanConfigured(ctx context.Context, store *inventory.Store) bool {
	_, err := store.LastSuccessfulScanAt(ctx, dbtype.ScanPorts)

	return err == nil
}

// ErrorPageData is the response.WithErrorDataFunc hook the server wires into
// the shared HTMLWriter, keyed by status the same way WithErrorTemplates is;
// each case supplies whatever its own template needs.
func ErrorPageData(_ *http.Request, _ error, status int) map[string]any {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		// A request the sender can fix and resend: it did not parse, was too
		// large, or failed validation. Rendered from layout/head like the 404
		// case below, since almost every form that reaches it is behind the
		// signed-in shell; setup and sign-in guard their own inputs in the
		// markup, so a crafted request is the only way they land here.
		return shellData("Bad request")
	case http.StatusUnauthorized:
		// The sign-in page's own fields (see loginData), since TemplateLogin
		// renders standalone like login itself does.
		return map[string]any{
			"Title": "Sign in",
			"Error": "That username and password do not match. Check both and try again.",
		}
	case http.StatusPreconditionRequired:
		// The second-factor page's own fields (see totpData), since
		// TemplateTOTP renders standalone too.
		return map[string]any{
			"Title": "Enter your code",
			"Error": "That code did not work. Enter the code your authenticator app shows now, or a recovery code.",
		}
	case http.StatusConflict:
		// The setup page's own fields, for the reason the 401 case above uses
		// loginData: TemplateSetup renders standalone too.
		return map[string]any{
			"Title": "Set up admin account",
			"Error": "An admin account already exists. Sign in with it.",
		}
	case http.StatusForbidden:
		// Forbidden renders inside the signed-in shell, since the visitor
		// reaching it is signed in, so it needs view's fields the same way the
		// 404 case below does.
		return shellData("Forbidden")
	default:
		// The 404 page is built from layout/head and layout/foot like every
		// other page, so it needs the same view fields.
		return shellData("Not found")
	}
}

// shellData is an error page's fields for a template rendered inside the
// signed-in shell: view's fields, every one beyond Title at its zero value.
func shellData(title string) map[string]any {
	return map[string]any{
		"Title":      title,
		"Section":    "",
		"Crumb":      nil,
		"Live":       "",
		"Role":       dbtype.UserRole(""),
		"SignedInAs": "",
		"Note":       "",
	}
}

// withQuery is path with q as its query string, or path alone when q is empty.
func withQuery(path string, q url.Values) string {
	return (&url.URL{Path: path, RawQuery: q.Encode()}).String()
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
