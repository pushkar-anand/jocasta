package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/server"
)

// Link sqlc with go generate, now we need to just run go generate to generate models and functions for DB
//go:generate sqlc generate -f ./../../sqlc.yaml

func main() {
	ctx := context.Background()

	// Create a context that will be canceled when the OS sends a signal to the process.
	// This will be used to gracefully shut down the application, shutting down the server and other workers.
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGINT, syscall.SIGABRT, syscall.SIGTERM)
	defer cancel()

	log := logger.New(
		logger.WithLevel(slog.LevelDebug),
		logger.WithFormat(logger.FormatJSON),
	)

	_, err := db.New(&db.Config{Path: "/home/pushkar/Projects/jocasta", Name: "jocasta.db"})
	if err != nil {
		log.ErrorContext(ctx, "failed to initialize database", logger.Err(err))
		return
	}

	err = server.Start(ctx, &server.Config{
		Addr:   "0.0.0.0",
		Port:   8080,
		Logger: log,
	})
	if err != nil {
		log.ErrorContext(ctx, "failed to start server", logger.Err(err))
		return
	}
}
