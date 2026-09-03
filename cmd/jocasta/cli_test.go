package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseCLI(t *testing.T, args []string) (*CLI, *kong.Context, error) {
	t.Helper()

	var cli CLI

	parser, err := newParser(&cli,
		kong.Exit(func(int) {}),
		kong.Writers(io.Discard, io.Discard),
	)
	require.NoError(t, err)

	kCtx, err := parser.Parse(args)

	return &cli, kCtx, err
}

func TestCLIDefaultServeCommand(t *testing.T) {
	t.Parallel()

	cli, kCtx, err := parseCLI(t, []string{})
	require.NoError(t, err)

	assert.Equal(t, "serve", kCtx.Command())
	assert.Equal(t, "jocasta.yaml", cli.ConfigFile)
	assert.Empty(t, cli.LogLevel)
	assert.Empty(t, cli.LogFormat)
	assert.Empty(t, cli.Serve.Host)
	assert.Zero(t, cli.Serve.Port)
}

func TestCLIServeFlags(t *testing.T) {
	t.Parallel()

	cli, kCtx, err := parseCLI(t, []string{
		"-c", "custom.yaml",
		"--log-level", "debug",
		"--log-format", "json",
		"serve",
		"--host", "0.0.0.0",
		"-p", "9090",
	})
	require.NoError(t, err)

	assert.Equal(t, "serve", kCtx.Command())
	assert.Equal(t, "custom.yaml", cli.ConfigFile)
	assert.Equal(t, "debug", cli.LogLevel)
	assert.Equal(t, "json", cli.LogFormat)
	assert.Equal(t, "0.0.0.0", cli.Serve.Host)
	assert.Equal(t, 9090, cli.Serve.Port)
}

func TestCLIVersionCommand(t *testing.T) {
	t.Parallel()

	_, kCtx, err := parseCLI(t, []string{"version"})
	require.NoError(t, err)

	assert.Equal(t, "version", kCtx.Command())
}

func TestCLIScanCommandDefaults(t *testing.T) {
	t.Parallel()

	cli, kCtx, err := parseCLI(t, []string{"scan", "192.168.1.0/24"})
	require.NoError(t, err)

	assert.Equal(t, "scan <target>", kCtx.Command())
	assert.Equal(t, "192.168.1.0/24", cli.Scan.Target)
	assert.Equal(t, 1000, cli.Scan.Rate)
	assert.Equal(t, 2, cli.Scan.Rounds)
	assert.Equal(t, 2*time.Second, cli.Scan.Wait)
	assert.True(t, cli.Scan.ResolveNames)
	assert.True(t, cli.Scan.ResolveMACs)
	assert.False(t, cli.Scan.JSON)
}

func TestCLIScanCommandCustomFlags(t *testing.T) {
	t.Parallel()

	cli, kCtx, err := parseCLI(t, []string{
		"scan", "10.0.0.0/16",
		"--rate", "500",
		"--rounds", "5",
		"--wait", "500ms",
		"--no-resolve-names",
		"--no-resolve-macs",
		"--json",
	})
	require.NoError(t, err)

	assert.Equal(t, "scan <target>", kCtx.Command())
	assert.Equal(t, "10.0.0.0/16", cli.Scan.Target)
	assert.Equal(t, 500, cli.Scan.Rate)
	assert.Equal(t, 5, cli.Scan.Rounds)
	assert.Equal(t, 500*time.Millisecond, cli.Scan.Wait)
	assert.False(t, cli.Scan.ResolveNames)
	assert.False(t, cli.Scan.ResolveMACs)
	assert.True(t, cli.Scan.JSON)
}

func TestCLIScanCommandMissingTarget(t *testing.T) {
	t.Parallel()

	_, _, err := parseCLI(t, []string{"scan"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected \"<target>\"")
}

func TestOutputScanResultsTable(t *testing.T) {
	t.Parallel()

	now := time.Now()
	swept := []scanner.Host{
		{Host: host("192.168.1.1", "00:00:0c:11:22:33", "router.lan", ""), RTT: 1200 * time.Microsecond, SeenAt: now},
		{Host: host("192.168.1.100", "02:00:5e:10:00:01", "", "eth0"), RTT: 500 * time.Microsecond, SeenAt: now, Self: true},
		{Host: host("192.168.1.101", "da:a1:19:00:11:22", "", ""), RTT: 900 * time.Microsecond, SeenAt: now},
	}

	var buf bytes.Buffer

	err := outputScanResults(&buf, swept, false)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "192.168.1.1")
	assert.Contains(t, output, "00:00:0c:11:22:33")
	assert.Contains(t, output, "Cisco")
	assert.Contains(t, output, "router.lan")

	assert.Contains(t, output, "192.168.1.100")
	assert.Contains(t, output, "[randomised]")
	assert.Contains(t, output, "self (eth0)")

	// A locally administered prefix the table names is that vendor's hardware,
	// so the placeholder is for addresses nothing can name, not for every
	// address a device assigned itself.
	assert.Contains(t, output, "192.168.1.101")
	assert.Contains(t, output, "Google")
}

func TestOutputScanResultsEmpty(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := outputScanResults(&buf, []scanner.Host{}, false)
	require.NoError(t, err)

	assert.Equal(t, "No responsive hosts found.\n", buf.String())
}

func TestOutputScanResultsJSON(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Second)
	swept := []scanner.Host{
		{Host: host("192.168.1.1", "00:00:0c:11:22:33", "router.lan", ""), RTT: time.Millisecond, SeenAt: now},
	}

	var buf bytes.Buffer

	err := outputScanResults(&buf, swept, true)
	require.NoError(t, err)

	// Decoded into the wire shape rather than back into a Host: the enriched
	// values live in unexported fields, so a round trip would assert against
	// whatever the decoder could not fill in.
	var decoded []struct {
		Addr     string        `json:"addr"`
		MAC      string        `json:"mac"`
		Vendor   string        `json:"vendor"`
		Hostname string        `json:"hostname"`
		RTT      time.Duration `json:"rtt"`
		SeenAt   time.Time     `json:"seen_at"`
	}

	err = json.Unmarshal(buf.Bytes(), &decoded)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	assert.Equal(t, "192.168.1.1", decoded[0].Addr)
	assert.Equal(t, "00:00:0c:11:22:33", decoded[0].MAC)
	assert.Equal(t, "Cisco", decoded[0].Vendor)
	assert.Equal(t, "router.lan", decoded[0].Hostname)
	assert.Equal(t, time.Millisecond, decoded[0].RTT)
	assert.Equal(t, now.UTC(), decoded[0].SeenAt.UTC())
}

func TestScanCmdInvalidTarget(t *testing.T) {
	t.Parallel()

	cmd := ScanCmd{Target: "invalid-cidr"}
	err := cmd.Run(t.Context(), nil, nil, nil, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "invalid CIDR prefix")
}

// A successful run of the scan command needs an ICMP socket, which CI does not
// grant, so run is covered through the failure paths that stop before the
// sweep. internal/scanner's TestSweepLive is the opt-in live sweep.
func TestRunRejectsMissingExplicitConfigFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "absent.yaml")

	err := run([]string{"--config", path, "scan", "127.0.0.1/32"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "absent.yaml")
}

func TestLoadConfigAllowsAbsentDefaultFile(t *testing.T) {
	cfg, err := loadConfig(&CLI{ConfigFile: config.DefaultConfigFile})
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestLoadConfigAppliesLoggingOverrides(t *testing.T) {
	cfg, err := loadConfig(&CLI{
		ConfigFile: config.DefaultConfigFile,
		LogLevel:   "debug",
		LogFormat:  "text",
	})
	require.NoError(t, err)

	assert.Equal(t, "debug", cfg.Logger.Level)
	assert.Equal(t, "text", cfg.Logger.Format)
}

// host builds an enriched host the way a sweep does. A malformed argument is a
// broken test.
func host(ip, mac, hostname, iface string) *hosts.Host {
	h, err := hosts.BuildHost(context.Background(), hosts.HostInput{
		IP:        ip,
		MAC:       mac,
		Hostname:  hostname,
		Interface: iface,
	})
	if err != nil {
		panic(err)
	}

	return h
}

func TestCLIPluginRunCommand(t *testing.T) {
	t.Parallel()

	cli, kCtx, err := parseCLI(t, []string{"plugin", "run", "gateway", "--save", "--json"})
	require.NoError(t, err)

	assert.Equal(t, "plugin run <name>", kCtx.Command())
	assert.Equal(t, "gateway", cli.Plugin.Run.Name)
	assert.True(t, cli.Plugin.Run.Save)
	assert.True(t, cli.Plugin.Run.JSON)
}

func TestCLIPluginRunNeedsAName(t *testing.T) {
	t.Parallel()

	_, _, err := parseCLI(t, []string{"plugin", "run"})
	require.Error(t, err)
}

// The builder skips a disabled instance and orders what is left, so a poller
// reads its sources the same way on every cycle.
func TestHostDiscoverersSkipsDisabledInstances(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Plugins.RouterOS = map[string]config.RouterOS{
		"rack":    {Enabled: true, Host: "198.51.100.1"},
		"gateway": {Enabled: true, Host: "192.0.2.1"},
		"spare":   {Enabled: false, Host: "203.0.113.1"},
	}

	ds, err := hostDiscoverers(cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	names := make([]string, len(ds))
	for i, d := range ds {
		names[i] = d.Name()
	}

	assert.Equal(t, []string{"routeros:gateway", "routeros:rack"}, names)
}

// A misconfigured entry is a config error rather than a source that stays
// quiet, which would look exactly like a network with nothing on it.
func TestHostDiscoverersRejectsAnInstanceWithNoHost(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Plugins.RouterOS = map[string]config.RouterOS{
		"gateway": {Enabled: true},
	}

	_, err := hostDiscoverers(cfg, slog.New(slog.DiscardHandler))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway")
}

func TestOutputFactsTable(t *testing.T) {
	t.Parallel()

	h, err := hosts.BuildHost(t.Context(), hosts.HostInput{
		IP: "192.0.2.10", MAC: "00:00:5e:00:53:01", Hostname: "host-a.example.com",
	})
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, outputFacts(&buf, []plugin.Fact{
		{Host: h, Present: true, HostnameSource: dbtype.HostnameFromDHCPStatic},
	}, false))

	out := buf.String()
	assert.Contains(t, out, "192.0.2.10")
	assert.Contains(t, out, "00:00:5e:00:53:01")
	assert.Contains(t, out, "host-a.example.com")
	assert.Contains(t, out, "DHCP_STATIC")
}

func TestOutputFactsEmpty(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, outputFacts(&buf, nil, false))

	assert.Contains(t, buf.String(), "no devices")
}
