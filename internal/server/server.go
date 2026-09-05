// Package server wires the API and web handlers into one HTTP server.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"

	"github.com/pushkar-anand/build-with-go/http/middleware"
	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/http/server"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/api"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/web"
	"github.com/rs/cors"
)

type (
	// Config controls how the server binds and what it logs to.
	Config struct {
		Port   int
		Addr   string
		Logger *slog.Logger

		// CORSAllowedOrigins lists the origins (scheme://host[:port]) a browser
		// may read this server's responses from cross-origin. Empty defaults to
		// this server's own address, which is the same thing a browser already
		// gets for free by same-origin rules -- CORS only starts to matter once
		// something outside that address needs in.
		CORSAllowedOrigins []string
	}
)

// Start runs the HTTP server for the API and web UI until ctx is cancelled.
//
// The store arrives built rather than opened here: the poller writes through
// the same one, and how the inventory is read must not depend on which caller
// constructed it.
func Start(
	ctx context.Context,
	cfg *Config,
	store *inventory.Store,
	validator *validator.Validator,
	a *auth.Auth,
) error {
	reader := request.NewReader(
		cfg.Logger,
		validator,
		request.WithRejectUnknownFields(),
		request.WithMaxBodyBytes(maxRequestBodyBytes),
	)
	sm := auth.NewSession()

	jw := response.NewJSONWriter(
		cfg.Logger,
		response.WithErrorProblemMapper(problemFor),
	)

	// The templates are set up in the web handler, passing nil here.
	hw := response.NewHTMLWriter(
		cfg.Logger,
		nil,
		response.WithErrorTemplates(map[int]string{
			http.StatusNotFound:     web.TemplateNotFound,
			http.StatusUnauthorized: web.TemplateLogin,
			http.StatusConflict:     web.TemplateSetup,
			http.StatusForbidden:    web.TemplateForbidden,
		}),
		response.WithErrorStatusMapper(func(err error) int {
			switch {
			case errors.Is(err, inventory.ErrNotFound):
				return http.StatusNotFound
			case errors.Is(err, auth.ErrInvalidCredentials):
				return http.StatusUnauthorized
			case errors.Is(err, auth.ErrSetupComplete):
				return http.StatusConflict
			case errors.Is(err, auth.ErrForbidden):
				return http.StatusForbidden
			}

			return http.StatusInternalServerError
		}),
		response.WithErrorDataFunc(web.ErrorPageData),
	)

	ap := api.NewHandler(cfg.Logger, reader, store, jw)
	wh := web.NewHandler(cfg.Logger, reader, store, hw, sm, a)

	tokenMiddleware := auth.NewTokenMiddleware(jw, a, regexp.MustCompile(`^/livez$`))
	sessionMiddleware := auth.NewSessionMiddleware(
		sm, a,
		[]*regexp.Regexp{regexp.MustCompile(`^/static/.*$`)},
		[]*regexp.Regexp{regexp.MustCompile(`^/login$`)},
	)

	mux := http.NewServeMux()

	// A script authenticates with a bearer token and carries no session
	// cookie; a browser tab carries a session cookie and never attaches a
	// bearer token on its own. Gating each mount with only the check its
	// caller can actually satisfy keeps that distinction enforced.
	mux.Handle("/api/", http.StripPrefix("/api", tokenMiddleware(ap)))
	mux.Handle("/", sessionMiddleware(wh))

	origins := cfg.CORSAllowedOrigins
	if len(origins) == 0 {
		origins = []string{fmt.Sprintf("http://%s:%d", cfg.Addr, cfg.Port)}
	}

	corsMW := cors.New(cors.Options{
		AllowedOrigins: origins,
		AllowedMethods: []string{http.MethodGet, http.MethodHead, http.MethodPatch},
		// Content-Type is covered by the library's own defaults; nothing here
		// asks for a header beyond what those already allow.
		ExposedHeaders: []string{"X-Request-Id"},
		MaxAge:         300,
	})

	// secureHeaders sits outside both gates so every response carries them,
	// including the static files the renderer never sees and the redirect an
	// unauthenticated request gets in place of a page. CORS sits inside
	// sameOrigin: it only ever adds Access-Control-* headers or answers a
	// preflight, never widens who may make a state-changing request -- that
	// stays sameOrigin's call.
	h := secureHeaders(sameOrigin(corsMW.Handler(logger.NewHTTPLogger(cfg.Logger)(mux))))
	h = middleware.RequestID(h)

	srv := server.New(
		sm.LoadAndSave(h),
		server.WithLogger(cfg.Logger),
		server.WithHostPort(cfg.Addr, cfg.Port),
	)

	return srv.Serve(ctx)
}

// maxRequestBodyBytes caps a PATCH body the reader will decode.
//
// The largest curationRequest/deviceEdit a caller can legitimately send is
// label(200) + notes(2000) + group(100) + a short type name -- a couple of KB
// even accounting for JSON or form-encoding overhead. 16KiB leaves an order of
// magnitude of headroom over that while still refusing an unbounded body.
const maxRequestBodyBytes = 16 << 10

// csp is the content security policy every response carries.
//
// Everything the pages load is served from this origin: the stylesheet, and the
// vendored htmx. That is the reason htmx is committed to the repository rather
// than pulled from a CDN, and the reason no markup here carries an inline style
// or an inline script -- both of which this policy refuses, and neither of which
// htmx needs so long as hx-on attributes are left alone.
const csp = "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// secureHeaders sets the response headers that are the same for every response.
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

// sameOrigin turns away a state-changing request that a browser has marked as
// coming from another site.
//
// This is not a CSRF token, and stands in for one: the web UI's session rides
// along on a cookie, which a browser attaches to a request regardless of which
// site asked for it, and this is what stops another site's page from spending
// that cookie's authority. The API's bearer token needs no such guard -- a
// browser never attaches an Authorization header on its own, so there is
// nothing here for a cross-site page to ride along on in the first place.
//
// The headers are only trusted when present. A browser always sends them; curl
// and the like send neither, and refusing those would break every script the
// JSON API exists for.
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

func fromSameOrigin(r *http.Request) bool {
	// Sec-Fetch-Site is the browser's own account of where the request came
	// from, and cannot be set by the page making it.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// Not a browser, or one too old to say. Origin is the older signal;
		// where it is absent too, there is nothing to check.
		break
	default:
		// cross-site or same-site: another origin, or another host on the same
		// registrable domain, which is not this one.
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

// problemFor renders the errors the inventory returns that are not simply
// failures. Anything else falls through to a generic 500, which is what an
// unexpected error deserves.
func problemFor(err error) response.Problem {
	if errors.Is(err, inventory.ErrNotFound) {
		return response.NewProblem().
			WithStatus(http.StatusNotFound).
			WithTitle(http.StatusText(http.StatusNotFound)).
			WithDetail(err.Error()).
			Build()
	}

	return nil
}
