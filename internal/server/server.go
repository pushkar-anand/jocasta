// Package server joins the API and web handlers into one HTTP server.
package server

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"time"

	"github.com/pushkar-anand/build-with-go/http/clientip"
	"github.com/pushkar-anand/build-with-go/http/middleware"
	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/http/server"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/api"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/mcp"
	"github.com/pushkar-anand/jocasta/internal/notify"
	"github.com/pushkar-anand/jocasta/internal/problem"
	"github.com/pushkar-anand/jocasta/internal/web"
	"github.com/rs/cors"
)

type (
	// Config controls how the server binds, and where it logs.
	Config struct {
		Port   int
		Addr   string
		Logger *slog.Logger

		// CORSAllowedOrigins lists the origins (scheme://host[:port]) from
		// which a browser can read the responses of this server cross-origin.
		// If it is empty, the server sends no CORS headers. Then only the
		// pages of the server can read them. CORS is important only when a
		// page at a different address needs access.
		CORSAllowedOrigins []string

		// SessionLifetime limits how long a browser stays signed in after
		// sign-in. SessionIdleTimeout ends the session after that time without
		// a request. SessionCookieSecure limits the session cookie to HTTPS.
		// It is false only for sign-in over plain HTTP during development.
		SessionLifetime     time.Duration
		SessionIdleTimeout  time.Duration
		SessionCookieSecure bool

		// MCPEnabled serves the Model Context Protocol endpoint at /mcp. When
		// it is off, the path answers that it is off.
		MCPEnabled bool

		// RecentTraffic is what the traffic recorder saw recently. The map
		// uses it to mark what is active. It is nil when no traffic source is
		// configured.
		RecentTraffic web.RecentTraffic

		// HomeCountry is where the world map starts its lines, when the
		// outside address of the router cannot give the country. It is empty
		// when not set.
		HomeCountry string

		// Notifier sends changes to the configured destinations, for the
		// notifications page. It is nil when no destination is enabled.
		Notifier *notify.Notifier

		// TrustedProxies lists the reverse proxy addresses or CIDR prefixes
		// whose forwarding headers the server trusts. When it is empty, the
		// client address is always the connection address.
		TrustedProxies []netip.Prefix
	}
)

// Start runs the HTTP server for the API and web UI until ctx is cancelled.
//
// The caller builds the store and gives it to Start. The poller writes through
// the same store. How the inventory is read must not depend on the caller
// that built it. conn is the same database. The session store uses it, so a
// sign-in stays after a restart.
func Start(
	ctx context.Context,
	cfg *Config,
	conn *sql.DB,
	store *inventory.Store,
	validator *validator.Validator,
	a *auth.Auth,
) error {
	srv := server.New(
		Handler(cfg, conn, store, validator, a),
		server.WithLogger(cfg.Logger),
		server.WithHostPort(cfg.Addr, cfg.Port),
	)

	return srv.Serve(ctx)
}

// Handler is everything that Start serves, without the listener. So a browser
// test can use the same routes and headers as a deployment.
func Handler(
	cfg *Config,
	conn *sql.DB,
	store *inventory.Store,
	validator *validator.Validator,
	a *auth.Auth,
) http.Handler {
	reader := request.NewReader(
		cfg.Logger,
		validator,
		request.WithRejectUnknownFields(),
		request.WithMaxBodyBytes(maxRequestBodyBytes),
	)
	sm := auth.NewSession(cfg.Logger,
		auth.WithSessionStore(conn),
		auth.WithLifetime(cfg.SessionLifetime),
		auth.WithIdleTimeout(cfg.SessionIdleTimeout),
		auth.WithCookieSecure(cfg.SessionCookieSecure),
	)

	jw := response.NewJSONWriter(
		cfg.Logger,
		response.WithErrorProblemMapper(problem.For),
	)

	// The web handler sets up the templates, so this passes nil.
	hw := response.NewHTMLWriter(
		cfg.Logger,
		nil,
		response.WithErrorTemplates(map[int]string{
			http.StatusBadRequest:            web.TemplateBadRequest,
			http.StatusRequestEntityTooLarge: web.TemplateBadRequest,
			http.StatusUnprocessableEntity:   web.TemplateBadRequest,
			http.StatusNotFound:              web.TemplateNotFound,
			http.StatusUnauthorized:          web.TemplateLogin,
			http.StatusPreconditionRequired:  web.TemplateTOTP,
			http.StatusTooManyRequests:       web.TemplateLogin,
			http.StatusConflict:              web.TemplateSetup,
			http.StatusForbidden:             web.TemplateForbidden,
		}),
		response.WithErrorStatusMapper(func(err error) int {
			switch {
			case errors.Is(err, inventory.ErrNotFound), errors.Is(err, auth.ErrNoTOTPEnrollment):
				return http.StatusNotFound
			case errors.Is(err, auth.ErrInvalidCredentials):
				return http.StatusUnauthorized
			// ErrInvalidTOTPCode is the only failure that needs its own page,
			// TemplateTOTP, with status 428. Nothing else in the app uses 428.
			// Browsers and proxies do not give it the special handling that
			// they give 401 and 407.
			case errors.Is(err, auth.ErrInvalidTOTPCode):
				return http.StatusPreconditionRequired
			case errors.Is(err, auth.ErrLoginLocked), errors.Is(err, auth.ErrTOTPLocked):
				return http.StatusTooManyRequests
			// Settings handlers render these next to the failed field. Also
			// keep the status mapping for errors from other handlers.
			case errors.Is(err, auth.ErrInvalidEnrollmentCode), errors.Is(err, auth.ErrInvalidPassword):
				return http.StatusUnprocessableEntity
			case errors.Is(err, auth.ErrSetupComplete):
				return http.StatusConflict
			case errors.Is(err, auth.ErrForbidden):
				return http.StatusForbidden
			}

			// With zero, the writer sets the status. Some errors describe
			// their own status, as the parse and validation failures of the
			// request package do. The writer reads that status. Only an
			// error with no mapping becomes a 500.
			return 0
		}),
		response.WithErrorDataFunc(web.ErrorPageData(sm)),
	)

	ap := api.NewHandler(cfg.Logger, reader, store, jw)

	var webOpts []web.Option
	if cfg.RecentTraffic != nil {
		webOpts = append(webOpts, web.WithRecentTraffic(cfg.RecentTraffic))
	}

	if cfg.HomeCountry != "" {
		webOpts = append(webOpts, web.WithHomeCountry(cfg.HomeCountry))
	}

	if cfg.Notifier != nil {
		webOpts = append(webOpts, web.WithNotifier(cfg.Notifier))
	}

	wh := web.NewHandler(cfg.Logger, reader, store, hw, sm, a, webOpts...)

	tokenMiddleware := auth.NewTokenMiddleware(jw, a, auth.WithTokenBypass(regexp.MustCompile(`^/livez$`)))
	sessionMiddleware := auth.NewSessionMiddleware(
		sm, a,
		[]*regexp.Regexp{regexp.MustCompile(`^/static/.*$`)},
		[]*regexp.Regexp{
			regexp.MustCompile(`^/login$`),
			regexp.MustCompile(`^/login/totp$`),
		},
	)

	mux := http.NewServeMux()

	// A script authenticates with a bearer token and has no session cookie.
	// A browser tab has a session cookie, and never adds a bearer token by
	// itself. Each mount has only the check that its caller can pass. This
	// keeps the two separate.
	mux.Handle("/api/", http.StripPrefix("/api", tokenMiddleware(ap)))
	mux.Handle("/", sessionMiddleware(wh))

	// For this reason, an agent is a script. /mcp accepts the same bearer
	// tokens. The MCP handler checks them itself, because the scope of a token
	// decides which tools the agent gets.
	if cfg.MCPEnabled {
		mux.Handle("/mcp", mcp.NewHandler(cfg.Logger, jw, a, store))
	} else {
		mux.Handle("/mcp", mcpDisabled(jw))
	}

	h := logger.NewHTTPLogger(cfg.Logger)(mux)

	// The client address goes into the context of every request. It comes
	// from the connection, or from the forwarding header of a trusted proxy.
	// It runs outside the access logger. So the log line shows the resolved
	// address.
	h = clientip.New(
		clientip.WithTrustedProxies(cfg.TrustedProxies...),
		clientip.WithLogger(cfg.Logger),
	).Middleware(h)

	// rs/cors reads an empty AllowedOrigins as every origin. So if none is
	// configured, the server leaves out the middleware. Then no response has
	// CORS headers.
	if len(cfg.CORSAllowedOrigins) > 0 {
		h = cors.New(cors.Options{
			AllowedOrigins: cfg.CORSAllowedOrigins,
			AllowedMethods: []string{http.MethodGet, http.MethodHead, http.MethodPatch},
			// The defaults of the library cover Content-Type. This code needs
			// no header beyond what they already allow.
			ExposedHeaders: []string{"X-Request-Id"},
			MaxAge:         300,
		}).Handler(h)
	}

	// secureHeaders is outside both gates, so every response has them. This
	// includes the static files that the renderer never sees. It also
	// includes the redirect that an unauthenticated request gets instead of a
	// page. CORS is inside sameOrigin. It only adds Access-Control-* headers
	// or answers a preflight. Only sameOrigin decides who can make a
	// state-changing request.
	h = secureHeaders(sameOrigin(h))
	h = middleware.RequestID(h)

	return sm.LoadAndSave(h)
}

// maxRequestBodyBytes limits the size of a PATCH body that the reader decodes.
//
// The largest valid curationRequest or deviceEdit is label(200) + notes(2000)
// + group(100) + a short type name. With JSON or form encoding, this is a few
// KB. 16KiB is ten times more than that. It still refuses a body with no
// limit.
const maxRequestBodyBytes = 16 << 10

// csp is the content security policy of every response.
//
// This origin serves everything that the pages load: the stylesheet, and the
// vendored htmx. For this reason, htmx is in the repository, and no markup here
// has an inline style or an inline script. This policy refuses both. htmx
// needs neither, while no hx-on attributes are in use.
const csp = "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// secureHeaders sets the headers that are the same for every response.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")

		next.ServeHTTP(w, r)
	})
}

// sameOrigin refuses a state-changing request that a browser marked as coming
// from a different site.
//
// This replaces a CSRF token. The session of the web UI is in a cookie. A
// browser adds the cookie to a request from any site. This check stops the
// page of a different site from using the authority of that cookie. The
// bearer token of the API needs no such guard. A browser never adds an
// Authorization header by itself. So a cross-site page cannot use it.
//
// The check trusts the headers only when they are present. A browser always
// sends them. curl and similar tools send neither. If the check refused those
// requests, every script for the JSON API would break.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) && !fromSameOrigin(r) {
			http.Error(w, "Cross-origin request refused", http.StatusForbidden)

			return
		}

		next.ServeHTTP(w, r)
	})
}

// safeMethod reports whether the method only reads.
func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}

	return false
}

// fromSameOrigin reports whether the browser headers of the request allow a
// write. It allows a request with no fetch metadata and no Origin. If it uses
// Origin, Origin must name the host of the request.
func fromSameOrigin(r *http.Request) bool {
	// Sec-Fetch-Site is where the browser says that the request came from.
	// The page that makes the request cannot set it.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// Not a browser, or a browser that is too old. Origin is the older
		// signal. If it is also absent, there is nothing to check.
		break
	default:
		// cross-site or same-site: a different origin, or a different host on
		// the same registrable domain.
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	return u.Host == r.Host
}

// mcpDisabled answers /mcp while the endpoint is off. Without it, the path
// would go to the web UI. An agent would then get the sign-in page, which does
// not tell why.
func mcpDisabled(jw *response.JSONWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jw.WriteProblem(w, r, response.NewProblem().
			WithStatus(http.StatusNotFound).
			WithTitle(http.StatusText(http.StatusNotFound)).
			WithDetail("the MCP endpoint is disabled; set server.mcp.enabled to serve it").
			Build())
	})
}
