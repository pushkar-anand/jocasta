package config

import (
	"log/slog"
	"os"
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

// TestLoadConfig checks that the defaults reach the struct, and that the
// environment overrides them.
//
// config.Load merges into one koanf store for the whole process. Each load
// adds to the one before. So this must stay the only Load in the package.
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
				"JOCASTA_SERVER__MCP__ENABLED=true",
				"JOCASTA_PLUGINS__NETFLOW__GATEWAY__LISTEN=:9995",
				"JOCASTA_LOCATION__COUNTRY=au",
				"JOCASTA_LOCATION__TIMEZONE=Australia/Sydney",
				"JOCASTA_RETENTION__HISTORY=48h",
				"JOCASTA_NOTIFY__PHONE__NTFY__TOKEN=from-environment",
				"UNRELATED=ignored",
			}
		}),
	)
	require.NoError(t, err)

	// From the environment.
	assert.Equal(t, 9999, cfg.Server.Port)
	assert.Equal(t, "debug", cfg.Logger.Level)

	// The environment does not set these, so they keep the defaults.
	assert.Equal(t, "localhost", cfg.Server.Host)
	assert.Equal(t, ".", cfg.DB.Path)
	assert.Equal(t, "jocasta.db", cfg.DB.Name)
	assert.Equal(t, "json", cfg.Logger.Format)

	// The config gives a duration as text. It must reach the struct as a
	// duration.
	assert.Equal(t, inventory.DefaultOnlineWindow, cfg.Inventory.OnlineWindow)
	assert.Equal(t, inventory.DefaultAddressGrace, cfg.Inventory.AddressGrace)

	// The default comes from the host, so assert how it is made. A source
	// with no name would put every sweep under one blank row.
	assert.Equal(t, defaultSource(), cfg.Scan.Source)

	// The mDNS, NetBIOS, DNS-SD and SSDP lookups are on by default.
	assert.True(t, cfg.Scan.Devices.ResolveMDNS)
	assert.True(t, cfg.Scan.Devices.ResolveNetBIOS)
	assert.True(t, cfg.Scan.Devices.ResolveDNSSD)
	assert.True(t, cfg.Scan.Devices.ResolveSSDP)

	// By default, the port scan is off, with a six-hour interval. The
	// environment turns it on and gives it a spec.
	assert.True(t, cfg.Scan.Ports.Enabled)
	assert.Equal(t, 6*time.Hour, cfg.Scan.Ports.Interval)
	assert.Equal(t, "22,80,8000-8100", cfg.Scan.Ports.Custom)

	// The environment does not set Concurrency here. So it keeps the default
	// of the scanner. A zero would mean "no limit".
	assert.Equal(t, scanner.DefaultConcurrency, cfg.Scan.Ports.Concurrency)

	// The auth settings are two durations as text, and a bool that is on by
	// default. All three must reach the struct from the defaults.
	assert.Equal(t, 168*time.Hour, cfg.Server.Auth.SessionLifetime)
	assert.Equal(t, 24*time.Hour, cfg.Server.Auth.IdleTimeout)
	assert.True(t, cfg.Server.Auth.CookieSecure)

	// MCP is off by default. See TestMCPIsOffByDefault. The environment turns
	// it on.
	assert.True(t, cfg.Server.MCP.Enabled)

	// If a map-keyed block changes to a list, it collapses to one zero-valued
	// entry, with a nil error. An override for one instance keeps both
	// instances. This shows that the block is still a map.
	require.Len(t, cfg.Plugins.RouterOS, 2)

	gateway := cfg.Plugins.RouterOS["gateway"]
	assert.True(t, gateway.Enabled)
	assert.Equal(t, "192.0.2.1", gateway.Host)
	assert.Equal(t, "jocasta", gateway.User)
	assert.True(t, gateway.SSL)
	assert.True(t, gateway.Insecure)
	assert.Equal(t, 15*time.Second, gateway.Timeout)

	// The override reaches the instance that it names, and only that one.
	assert.Equal(t, "from-environment", gateway.Password)

	rack := cfg.Plugins.RouterOS["switch_rack"]
	assert.False(t, rack.Enabled)
	assert.Equal(t, "198.51.100.1", rack.Host)
	assert.Equal(t, 8080, rack.Port)
	assert.Equal(t, "also-from-file", rack.Password)
	assert.True(t, rack.TopologyOnly)
	assert.False(t, gateway.TopologyOnly)

	// A NetFlow instance is map-keyed for the same reason. An override of its
	// listen address keeps the exporters from the file.
	require.Len(t, cfg.Plugins.NetFlow, 1)

	flows := cfg.Plugins.NetFlow["gateway"]
	assert.True(t, flows.Enabled)
	assert.Equal(t, ":9995", flows.Listen)
	assert.Equal(t, []string{"192.0.2.1"}, flows.Exporters)

	// Each retention window is a separate duration. One has an override. The
	// others keep their defaults.
	assert.Equal(t, 48*time.Hour, cfg.Retention.History)
	assert.Equal(t, inventory.DefaultTrafficRetention, cfg.Retention.Traffic)
	assert.Equal(t, inventory.DefaultDNSRetention, cfg.Retention.DNS)
	assert.Equal(t, inventory.DefaultDeviceRetention, cfg.Retention.Devices)

	assert.Equal(t, "au", cfg.Location.Country, "as written; serve checks it")
	assert.Equal(t, "Australia/Sydney", cfg.Location.Timezone)

	// Notification destinations also have their name as key. An override for
	// one does not change the other.
	require.Len(t, cfg.Notify, 3)

	phone := cfg.Notify["phone"]
	assert.True(t, phone.On(), "on when enabled is left out")
	require.NotNil(t, phone.Ntfy)
	assert.Nil(t, phone.Webhook)
	assert.Equal(t, "https://ntfy.example.com/jocasta", phone.Ntfy.URL)
	assert.Equal(t, "from-environment", phone.Ntfy.Token)
	assert.Equal(t, 4, phone.Ntfy.Priority)

	automation := cfg.Notify["automation"]
	assert.False(t, automation.On())
	assert.Equal(t, 5*time.Second, automation.Timeout)
	require.NotNil(t, automation.Webhook)
	assert.Equal(t, "placeholder-secret", automation.Webhook.Secret)

	// An http destination keeps its headers and its body template as
	// written.
	chat := cfg.Notify["chat"]
	require.NotNil(t, chat.HTTP)
	assert.Equal(t, "https://chat.example.com/hook", chat.HTTP.URL)
	assert.Equal(t, `{"text": {{ json .Title }}}`, chat.HTTP.Body)
	require.Len(t, chat.HTTP.Headers, 1)

	for k, v := range chat.HTTP.Headers {
		assert.True(t, strings.EqualFold("X-Api-Key", k), k)
		assert.Equal(t, "placeholder-key", v)
	}
}

// New reports an explicit path that does not exist. Defaults with no warning
// could bind the server to the wrong address.
func TestNewRejectsAMissingConfigFile(t *testing.T) {
	t.Parallel()

	_, err := New("testdata/does-not-exist.yaml")
	require.Error(t, err)
}

// The owner must turn on the MCP server for AI agents. This test reads the
// defaults map directly, because TestLoadConfig must be the only caller of
// config.Load.
func TestMCPIsOffByDefault(t *testing.T) {
	t.Parallel()

	assert.Equal(t, false, defaults["server.mcp.enabled"])
}

func TestDefaultSourceNamesTheHost(t *testing.T) {
	t.Parallel()

	host, err := os.Hostname()
	require.NoError(t, err)

	assert.Equal(t, "sweep:"+host, defaultSource())
}
