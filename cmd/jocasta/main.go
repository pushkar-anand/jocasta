package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/pushkar-anand/build-with-go/http/server"
	"github.com/pushkar-anand/build-with-go/logger"
)

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

	mux := http.NewServeMux()

	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello, World"))
	})

	srv := server.New(
		mux,
		server.WithLogger(log),
	)

	if err := srv.Serve(ctx); err != nil {
		log.ErrorContext(ctx, "failed", logger.Error(err))
		return
	}
}
