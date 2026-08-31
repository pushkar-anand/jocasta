package main

import (
	"log/slog"
	"testing"

	"github.com/pushkar-anand/build-with-go/config"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoggerSlogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level string
		want  slog.Level
	}{
		{level: "debug", want: slog.LevelDebug},
		{level: "DEBUG", want: slog.LevelDebug},
		{level: "info", want: slog.LevelInfo},
		{level: "warn", want: slog.LevelWarn},
		{level: "warning", want: slog.LevelWarn},
		{level: "WARNING", want: slog.LevelWarn},
		{level: "error", want: slog.LevelError},
		{level: "Error", want: slog.LevelError},
		{level: "", want: slog.LevelInfo},
		{level: "nonsense", want: slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, Logger{Level: tt.level}.SlogLevel())
		})
	}
}

func TestLoggerFormatValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format string
		want   logger.Format
	}{
		{format: "text", want: logger.FormatText},
		{format: "TEXT", want: logger.FormatText},
		{format: "json", want: logger.FormatJSON},
		{format: "", want: logger.FormatJSON},
		{format: "nonsense", want: logger.FormatJSON},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, Logger{Format: tt.format}.FormatValue())
		})
	}
}

// TestDefaults pins the values the application falls back to when neither the
// YAML file nor the environment supplies one.
func TestDefaults(t *testing.T) {
	t.Parallel()

	assert.Equal(t, map[string]any{
		"server.host":   "localhost",
		"server.port":   8080,
		"db.path":       ".",
		"db.name":       "jocasta.db",
		"logger.level":  "info",
		"logger.format": "json",

		"inventory.online_window": "15m0s",
	}, defaults)
}

// TestLoadConfig checks that the defaults reach the struct and that the
// environment layers over them.
//
// config.Load merges into a process-wide koanf store, so this must stay the
// only Load in the package: successive loads are cumulative, not independent.
func TestLoadConfig(t *testing.T) {
	cfg, err := config.Load[Config](
		config.WithDefaults(defaults),
		config.WithYAML("testdata/does-not-exist.yaml"),
		config.WithEnvPrefix("JOCASTA_"),
		config.WithEnviron(func() []string {
			return []string{
				"JOCASTA_SERVER__PORT=9999",
				"JOCASTA_LOGGER__LEVEL=debug",
				"UNRELATED=ignored",
			}
		}),
	)
	require.NoError(t, err)

	// From the environment.
	assert.Equal(t, 9999, cfg.Server.Port)
	assert.Equal(t, "debug", cfg.Logger.Level)

	// Untouched by the environment, so still the defaults.
	assert.Equal(t, "localhost", cfg.Server.Host)
	assert.Equal(t, ".", cfg.DB.Path)
	assert.Equal(t, "jocasta.db", cfg.DB.Name)
	assert.Equal(t, "json", cfg.Logger.Format)

	// A duration is configured as text and has to reach the struct as one.
	assert.Equal(t, inventory.DefaultOnlineWindow, cfg.Inventory.OnlineWindow)
}
