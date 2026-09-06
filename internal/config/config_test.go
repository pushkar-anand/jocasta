package config

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pushkar-anand/build-with-go/config"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
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

// TestLoadConfig checks that the defaults reach the struct and that the
// environment layers over them.
//
// config.Load merges into a process-wide koanf store, so this must stay the
// only Load in the package: successive loads are cumulative, not independent.
func TestLoadConfig(t *testing.T) {
	cfg, err := config.Load[Config](
		config.WithDefaults(defaults),
		config.WithYAML("testdata/plugins.yaml"),
		config.WithEnvPrefix("JOCASTA_"),
		config.WithEnviron(func() []string {
			return []string{
				"JOCASTA_SERVER__PORT=9999",
				"JOCASTA_LOGGER__LEVEL=debug",
				"JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD=from-environment",
				"JOCASTA_SCAN__PORTS__ENABLED=true",
				"JOCASTA_SCAN__PORTS__CUSTOM=22,80,8000-8100",
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
	assert.Equal(t, inventory.DefaultAddressGrace, cfg.Inventory.AddressGrace)

	// Derived rather than written down, so assert it is the derivation and not
	// merely non-empty: an unnamed source files every sweep under one blank row.
	assert.Equal(t, defaultSource(), cfg.Scan.Source)

	// The port scan is off by default on a six-hour interval; the environment
	// turns it on and hands it a spec.
	assert.True(t, cfg.Scan.Ports.Enabled)
	assert.Equal(t, 6*time.Hour, cfg.Scan.Ports.Interval)
	assert.Equal(t, "22,80,8000-8100", cfg.Scan.Ports.Custom)

	// Concurrency is untouched by the environment here, so it stays the
	// scanner's default rather than falling to a zero that means "no limit".
	assert.Equal(t, scanner.DefaultConcurrency, cfg.Scan.Ports.Concurrency)

	// A map-keyed block collapses to a single zero-valued entry, with a nil
	// error, if its shape is ever changed to a list. Both instances surviving an
	// override aimed at one of them is what says it did not.
	require.Len(t, cfg.Plugins.RouterOS, 2)

	gateway := cfg.Plugins.RouterOS["gateway"]
	assert.True(t, gateway.Enabled)
	assert.Equal(t, "192.0.2.1", gateway.Host)
	assert.Equal(t, "jocasta", gateway.User)
	assert.True(t, gateway.SSL)
	assert.True(t, gateway.Insecure)
	assert.Equal(t, 15*time.Second, gateway.Timeout)

	// The override reaches the instance it names, and only that one.
	assert.Equal(t, "from-environment", gateway.Password)

	rack := cfg.Plugins.RouterOS["switch_rack"]
	assert.False(t, rack.Enabled)
	assert.Equal(t, "198.51.100.1", rack.Host)
	assert.Equal(t, 8080, rack.Port)
	assert.Equal(t, "also-from-file", rack.Password)
}

// An explicit path that does not exist is reported rather than silently falling
// back to defaults, which would bind the server to the wrong address.
func TestNewRejectsAMissingConfigFile(t *testing.T) {
	t.Parallel()

	_, err := New("testdata/does-not-exist.yaml")
	require.Error(t, err)
}

func TestDefaultSourceNamesTheHost(t *testing.T) {
	t.Parallel()

	host, err := os.Hostname()
	require.NoError(t, err)

	assert.Equal(t, "sweep:"+host, defaultSource())
}

// Each child process gets a fresh configuration loader; its state is global.
func TestMCPConfig(t *testing.T) {
	if os.Getenv("JOCASTA_CONFIG_TEST_CHILD") == "1" {
		cfg, err := New(DefaultConfigFile)
		require.NoError(t, err)
		assert.Equal(t, os.Getenv("JOCASTA_CONFIG_TEST_EXPECT") == "true", cfg.MCP.Enabled)

		return
	}

	for _, tc := range []struct {
		name, yaml, env string
		want            bool
	}{
		{name: "omitted", yaml: "server:\n  port: 8080\n"},
		{name: "disabled", yaml: "mcp:\n  enabled: false\n"},
		{name: "enabled", yaml: "mcp:\n  enabled: true\n", want: true},
		{name: "environment enables", yaml: "mcp:\n  enabled: false\n", env: "true", want: true},
		{name: "environment disables", yaml: "mcp:\n  enabled: true\n", env: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "jocasta.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.yaml), 0o600))

			exe, err := os.Executable()
			require.NoError(t, err)
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestMCPConfig$") //nolint:gosec // Runs this test executable, never a caller-supplied command.

			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "JOCASTA_") {
					cmd.Env = append(cmd.Env, entry)
				}
			}

			expected := "false"
			if tc.want {
				expected = "true"
			}

			cmd.Dir = filepath.Dir(path)

			cmd.Env = append(cmd.Env, "JOCASTA_CONFIG_TEST_CHILD=1", "JOCASTA_CONFIG_TEST_EXPECT="+expected)
			if tc.env != "" {
				cmd.Env = append(cmd.Env, "JOCASTA_MCP__ENABLED="+tc.env)
			}

			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}
