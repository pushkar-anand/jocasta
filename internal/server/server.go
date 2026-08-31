package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/middleware"
	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/server"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/api"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/web"
)

type (
	Config struct {
		Port   int
		Addr   string
		Logger *slog.Logger

		// OnlineWindow is how recently a device must have answered to be shown
		// as online. Zero leaves the inventory's own default in place.
		OnlineWindow time.Duration
	}
)

func Start(
	ctx context.Context,
	cfg *Config,
	conn *db.DB,
	validator *validator.Validator,
) error {
	reader := request.NewReader(cfg.Logger, validator, request.WithRejectUnknownFields())

	// One store for both surfaces: the JSON API and the web UI answer the same
	// questions, and reading them through one place is what keeps the answers
	// the same.
	store := inventory.New(conn.Conn, cfg.Logger, inventory.WithOnlineWindow(cfg.OnlineWindow))

	ap := api.NewHandler(cfg.Logger, reader, store)
	wh := web.NewHandler(cfg.Logger, reader, store)

	mux := http.NewServeMux()

	mux.Handle("/api/", http.StripPrefix("/api", ap))
	mux.Handle("/", wh)

	// Applied outside the logger so every response carries them, including the
	// static files, which the renderer never sees.
	h := secureHeaders(logger.NewHTTPLogger(cfg.Logger)(mux))
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
