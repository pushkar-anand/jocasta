package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/pushkar-anand/jocasta/internal/config"
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
	hosts := []scanner.Host{
		{
			Addr:     netip.MustParseAddr("192.168.1.1"),
			MAC:      "00:11:22:33:44:55",
			Vendor:   "MikroTik",
			Hostname: "router.lan",
			RTT:      1200 * time.Microsecond,
			SeenAt:   now,
		},
		{
			Addr:       netip.MustParseAddr("192.168.1.100"),
			MAC:        "da:a1:19:00:11:22",
			Randomised: true,
			Hostname:   "",
			RTT:        500 * time.Microsecond,
			SeenAt:     now,
			Self:       true,
			Interface:  "eth0",
		},
	}

	var buf bytes.Buffer

	err := outputScanResults(&buf, hosts, false)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "192.168.1.1")
	assert.Contains(t, output, "00:11:22:33:44:55")
	assert.Contains(t, output, "MikroTik")
	assert.Contains(t, output, "router.lan")

	assert.Contains(t, output, "192.168.1.100")
	assert.Contains(t, output, "[randomised]")
	assert.Contains(t, output, "self (eth0)")
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
	hosts := []scanner.Host{
		{
			Addr:     netip.MustParseAddr("192.168.1.1"),
			MAC:      "00:11:22:33:44:55",
			Vendor:   "MikroTik",
			Hostname: "router.lan",
			RTT:      time.Millisecond,
			SeenAt:   now,
		},
	}

	var buf bytes.Buffer

	err := outputScanResults(&buf, hosts, true)
	require.NoError(t, err)

	var decoded []scanner.Host

	err = json.Unmarshal(buf.Bytes(), &decoded)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	assert.Equal(t, hosts[0].Addr, decoded[0].Addr)
	assert.Equal(t, hosts[0].MAC, decoded[0].MAC)
	assert.Equal(t, hosts[0].Vendor, decoded[0].Vendor)
	assert.Equal(t, hosts[0].Hostname, decoded[0].Hostname)
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
