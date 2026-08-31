package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

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
	wh := web.NewHandler(cfg.Logger, conn, reader)

	mux := http.NewServeMux()

	mux.Handle("/api/", http.StripPrefix("/api", ap))
	mux.Handle("/", wh)

	h := logger.NewHTTPLogger(cfg.Logger)(mux)

	srv := server.New(
		h,
		server.WithLogger(cfg.Logger),
		server.WithHostPort(cfg.Addr, cfg.Port),
	)

	return srv.Serve(ctx)
}
