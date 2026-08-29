package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

	log.Info("test")

	fmt.Println("Hello World!")
}
