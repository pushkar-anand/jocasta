package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/server"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/api"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/web"
)

type (
	Config struct {
		Port   int
		Addr   string
		Logger *slog.Logger
	}
)

func Start(ctx context.Context, cfg *Config, conn *db.DB) error {
	ap := api.NewHandler(cfg.Logger, conn)
	wh := web.NewHandler(cfg.Logger, conn)

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
