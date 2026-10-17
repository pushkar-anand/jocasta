// Package web serves the inventory as HTML. Interactivity comes from htmx
// attributes on markup that the server renders. So a fragment endpoint and a
// page endpoint differ only in the template that they name.
package web

import (
	"context"
	"embed"
	"errors"
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

// Pages and partials parse into one template set. Every file has a single
// namespaced define (page/dashboard, partial/live, layout/head). This makes
// the flat set safe. Templates that parse together share one namespace. So if
// a name repeated across files, the last parsed file would replace the rest,
// with no warning.
//
//go:embed templates/pages/*.html.tmpl templates/partials/*.html.tmpl
var templatesFS embed.FS

// activityLimit is how much of the change log the overview shows. The full log
// has its own page.
const activityLimit = 12

// Handler serves the HTML UI over the same store as the JSON API.
type Handler struct {
	mux        *http.ServeMux
	store      *inventory.Store
	htmlWriter *response.HTMLWriter
	reader     *request.Reader
	log        *slog.Logger

	// recent is what the traffic recorder saw recently. It is nil when no
	// traffic source is configured.
	recent RecentTraffic

	// homeCountry is the configured country from which the world map draws.
	homeCountry string

	// notifier sends changes to the configured destinations. It is nil when
	// no destination is enabled.
	notifier *notify.Notifier
}

// ServeHTTP routes a request to the page or fragment handler that matches it.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// NewHandler builds the web routes. It parses the embedded templates, and
// mounts the static assets.
func NewHandler(
	log *slog.Logger,
	reader *request.Reader,
	store *inventory.Store,
	hw *response.HTMLWriter,
	sm *auth.Session,
	a *auth.Auth,
	opts ...Option,
) *Handler {
	// A template that does not parse is a broken build. Every template is
	// compiled into the binary.
	templates := template.Must(
		template.New("").
			Funcs(funcs(store.Now, store.OnlineWindow())).
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

	// allow wraps a handler. So only an account with at least role want
	// reaches it. Everyone else gets the forbidden page.
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

	// Sign-out changes state. So it is a POST, and the sameOrigin guard covers
	// it. A different page could use a link.
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

	// {$} matches only the root itself. So an unknown path reaches the
	// catch-all below, and gets not found.
	h.mux.HandleFunc("GET /{$}", hw.Handle(h.overview(sm)))
	h.mux.HandleFunc("GET /overview/live", hw.Handle(h.overviewLive()))

	// The literal is the more specific pattern. So it wins over {id}.
	h.mux.HandleFunc("GET /devices", hw.Handle(h.listDevices(sm, false)))
	h.mux.HandleFunc("GET /devices/rows", hw.Handle(h.listDevices(sm, true)))
	h.mux.HandleFunc("GET /devices/{id}", hw.Handle(h.device(sm)))
	h.mux.Handle("PATCH /devices/{id}", allow(dbtype.RoleReadWrite)(hw.Handle(h.updateDevice(sm))))
	h.mux.Handle("POST /devices/{id}/watch", allow(dbtype.RoleReadWrite)(hw.Handle(h.watchDevice(sm))))
	h.mux.HandleFunc("GET /devices/{id}/row", hw.Handle(h.deviceRow(sm)))
	h.mux.HandleFunc("GET /devices/{id}/traffic", hw.Handle(h.deviceTraffic()))
	h.mux.HandleFunc("GET /devices/{id}/dns", hw.Handle(h.deviceDNS()))
	h.mux.Handle("GET /devices/{id}/edit", allow(dbtype.RoleReadWrite)(hw.Handle(h.deviceRowForm())))
	h.mux.Handle("PATCH /devices/{id}/row", allow(dbtype.RoleReadWrite)(hw.Handle(h.updateDeviceRow(sm))))

	h.mux.HandleFunc("GET /networks/{id}", hw.Handle(h.network(sm, false)))
	h.mux.HandleFunc("GET /networks/{id}/rows", hw.Handle(h.network(sm, true)))

	h.mux.HandleFunc("GET /traffic", hw.Handle(h.traffic(sm)))
	h.mux.HandleFunc("GET /map", hw.Handle(h.networkMap(sm)))
	h.mux.HandleFunc("GET /map/live", hw.Handle(h.networkMapLive()))
	h.mux.HandleFunc("GET /topology", hw.Handle(h.topologyView(sm)))
	h.mux.HandleFunc("GET /topology/live", hw.Handle(h.topologyLive()))
	h.mux.HandleFunc("GET /events", hw.Handle(h.events(sm)))
	h.mux.HandleFunc("GET /scans", hw.Handle(h.scans(sm)))

	h.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		hw.ErrorPage(w, r, http.StatusNotFound)
	})

	return h
}

// latestSweepNote is the line at the bottom of the rail of every page. It is
// empty when there is no sweep yet, or the read failed. In both cases, the
// page leaves out the line.
func latestSweepNote(ctx context.Context, store *inventory.Store) string {
	scan, err := store.LatestScan(ctx)
	if err != nil {
		return ""
	}

	return sweepNote(store.Now(), scan)
}

func sweepNote(now time.Time, scan *inventory.Scan) string {
	// The rail labels the line "Last sweep". So the verb would repeat.
	note := ago(now, scan.StartedAt)

	// A collector can still record scan rows while each of them fails. Without
	// this, the line would still look healthy.
	if scan.Status == dbtype.StatusFailed {
		note += " · failed"
	}

	return note
}

// lastSweptAt is when a device sweep last finished with a result. It is zero
// before the first. The device page shows it next to the last_seen of the
// device. Together, they separate a device that left from sweeps that
// stopped.
func lastSweptAt(ctx context.Context, store *inventory.Store) time.Time {
	at, err := store.LastSuccessfulScanAt(ctx, dbtype.ScanDiscovery)
	if err != nil {
		return time.Time{}
	}

	return at
}

// portScanConfigured reports whether a port scan ever finished. It is a signal
// for the whole instance. It is enough for the Ports section to say "port
// scanning is not set up". Without it, the section would suggest that a scan
// found the device closed.
func portScanConfigured(ctx context.Context, store *inventory.Store) bool {
	_, err := store.LastSuccessfulScanAt(ctx, dbtype.ScanPorts)

	return err == nil
}

// ErrorPageData returns the response.WithErrorDataFunc hook that the server
// gives the shared HTMLWriter. Its key is the status, as for
// WithErrorTemplates. Each case gives what its own template needs. sm names
// the signed-in account for the pages inside the signed-in shell.
func ErrorPageData(sm *auth.Session) func(*http.Request, error, int) map[string]any {
	return func(r *http.Request, err error, status int) map[string]any {
		return errorPageData(sm, r, err, status)
	}
}

func errorPageData(sm *auth.Session, r *http.Request, err error, status int) map[string]any {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		// A request that the sender can fix and send again. It did not parse,
		// was too large, or failed validation. It renders from layout/head, as
		// the 404 case below does. Almost every form that reaches it is behind
		// the signed-in shell. Setup and sign-in guard their own inputs in the
		// markup. So only a crafted request from them reaches here.
		return shellData(sm, r, "Request could not be processed")
	case http.StatusUnauthorized:
		// The fields of the sign-in page (see loginData), because TemplateLogin
		// renders alone, as login itself does.
		return map[string]any{
			"Title":      "Error: Sign in",
			"Error":      "That username and password do not match. Check both and try again.",
			"Username":   r.PostForm.Get("username"),
			"RememberMe": r.PostForm.Get("remember_me") == "true",
		}
	case http.StatusPreconditionRequired:
		// The fields of the second-factor page (see totpData), because
		// TemplateTOTP also renders alone. The form posts its method. So a
		// recovery code that did not work comes back to the recovery form.
		return map[string]any{
			"Title":    "Error: Enter your code",
			"Error":    "That code did not work. Enter the code your authenticator app shows now, or a recovery code.",
			"Recovery": r.PostForm.Get("method") == totpMethodRecovery,
		}
	case http.StatusTooManyRequests:
		if errors.Is(err, auth.ErrLoginLocked) {
			// Too many passwords. The visitor is still on the sign-in page.
			// So the username stays as typed, as in the 401 case.
			return map[string]any{
				"Title":      "Error: Sign in",
				"Error":      "Too many sign-in attempts. Wait a minute, then try again.",
				"Username":   r.PostForm.Get("username"),
				"RememberMe": r.PostForm.Get("remember_me") == "true",
			}
		}

		// Too many second-factor codes. The pending sign-in ended. So the next
		// try starts on the sign-in page.
		return map[string]any{
			"Title":      "Error: Sign in",
			"Error":      "Too many codes did not work. Wait a few minutes, then try again.",
			"Username":   "",
			"RememberMe": false,
		}
	case http.StatusConflict:
		// The fields of the setup page, for the same reason as loginData in the
		// 401 case above. TemplateSetup also renders alone.
		return map[string]any{
			"Title": "Error: Set up admin account",
			"Error": "An admin account already exists. Sign in with it.",
		}
	case http.StatusForbidden:
		// Forbidden renders inside the signed-in shell, because its visitor is
		// signed in. So it needs the fields of view, as the 404 case below
		// does.
		return shellData(sm, r, "Permission needed")
	default:
		// The 404 page comes from layout/head and layout/foot, as every other
		// page does. So it needs the same view fields.
		return shellData(sm, r, "Not found")
	}
}

// shellData is the fields of an error page for a template inside the
// signed-in shell. These are the fields of view, with the signed-in account.
// Every other field after Title has its zero value.
func shellData(sm *auth.Session, r *http.Request, title string) map[string]any {
	ctx := r.Context()

	return map[string]any{
		"Title":      title,
		"Section":    "",
		"Crumb":      nil,
		"Live":       "",
		"Role":       sm.CurrentRole(ctx),
		"SignedInAs": sm.CurrentUsername(ctx),
		"Note":       "",
	}
}

// withQuery is path with q as its query string. It is only path when q is
// empty.
func withQuery(path string, q url.Values) string {
	return (&url.URL{Path: path, RawQuery: q.Encode()}).String()
}

// pathID reads the {id} that the route captured. Every route with one allows
// any segment. So this function refuses a value that is not a positive id. The
// caller decides how.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}

	return id, true
}
