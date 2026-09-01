// Package server wires the API and web handlers into one HTTP server.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/pushkar-anand/build-with-go/http/middleware"
	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/server"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/api"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/web"
)

type (
	// Config controls how the server binds and what it logs to.
	Config struct {
		Port   int
		Addr   string
		Logger *slog.Logger
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
) error {
	reader := request.NewReader(cfg.Logger, validator, request.WithRejectUnknownFields())

	ap := api.NewHandler(cfg.Logger, reader, store)
	wh := web.NewHandler(cfg.Logger, reader, store)

	mux := http.NewServeMux()

	mux.Handle("/api/", http.StripPrefix("/api", ap))
	mux.Handle("/", wh)

	// Applied outside the logger so every response carries them, including the
	// static files, which the renderer never sees.
	h := secureHeaders(sameOrigin(logger.NewHTTPLogger(cfg.Logger)(mux)))
	h = middleware.RequestID(h)

	srv := server.New(
		h,
		server.WithLogger(cfg.Logger),
		server.WithHostPort(cfg.Addr, cfg.Port),
	)

	return srv.Serve(ctx)
}

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

// sameOrigin turns away a state-changing request that a browser has told us
// came from another site.
//
// This is not a CSRF token, and cannot be one yet: a token has to be bound to a
// session, and there is no authentication to bind it to. Nor is one needed
// while there is none -- with no credential to ride along, there is nothing for
// a forged request to borrow. What it does do is refuse the shape of the attack
// in advance, so the guard is already in place when sessions arrive.
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
