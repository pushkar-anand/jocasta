package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/server"
)

type ServeCmd struct {
	Host string `name:"host" help:"Override server listen host."`
	Port int    `name:"port" short:"p" help:"Override server listen port."`
}

func (s *ServeCmd) Run(ctx context.Context, cfg *Config, log *slog.Logger) error {
	if s.Host != "" {
		cfg.Server.Host = s.Host
	}
	if s.Port != 0 {
		cfg.Server.Port = s.Port
	}

	_, err := db.New(&db.Config{Path: cfg.DB.Path, Name: cfg.DB.Name})
	if err != nil {
		log.ErrorContext(ctx, "failed to initialize database", logger.Err(err))
		return fmt.Errorf("initialize database: %w", err)
	}

	err = server.Start(ctx, &server.Config{
		Addr:   cfg.Server.Host,
		Port:   cfg.Server.Port,
		Logger: log,
	})
	if err != nil {
		log.ErrorContext(ctx, "failed to start server", logger.Err(err))
		return fmt.Errorf("start server: %w", err)
	}

	return nil
}
