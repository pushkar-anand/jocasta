package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/server"
)

type ServeCmd struct {
	Host string `name:"host" help:"Override server listen host."`
	Port int    `name:"port" short:"p" help:"Override server listen port."`
}

func (s *ServeCmd) Run(ctx context.Context, cfg *Config, log *slog.Logger) error {
	// The flags override the file, and an unset flag is its zero value.
	host := cmp.Or(s.Host, cfg.Server.Host)
	port := cmp.Or(s.Port, cfg.Server.Port)

	_, err := db.New(&db.Config{Path: cfg.DB.Path, Name: cfg.DB.Name})
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}

	err = server.Start(ctx, &server.Config{
		Addr:   host,
		Port:   port,
		Logger: log,
	})
	if err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	return nil
}
