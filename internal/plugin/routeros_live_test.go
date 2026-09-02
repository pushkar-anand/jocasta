package plugin

import (
	"log/slog"
	"os"
	"slices"
	"strconv"
	"testing"
	"text/tabwriter"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// The same variables pkg/routeros's live test reads, so one shell runs both.
const (
	liveHostEnv     = "JOCASTA_PLUGINS__ROUTEROS__HOST"
	livePortEnv     = "JOCASTA_PLUGINS__ROUTEROS__PORT"
	liveUserEnv     = "JOCASTA_PLUGINS__ROUTEROS__USER"
	livePasswordEnv = "JOCASTA_PLUGINS__ROUTEROS__PASSWORD" //nolint:gosec // the name of a variable, not a credential.
	liveSSLEnv      = "JOCASTA_PLUGINS__ROUTEROS__SSL"
	liveInsecureEnv = "JOCASTA_PLUGINS__ROUTEROS__INSECURE"
)

// TestDiscoverLive runs the mapping over a real router's tables and prints the
// facts. Unlike pkg/routeros's live test, which shows what the wire returned,
// this shows what ingest would be handed.
//
// It is skipped without a host: the rest of the suite must stay hermetic.
//
//	JOCASTA_PLUGINS__ROUTEROS__HOST=192.0.2.1 \
//	JOCASTA_PLUGINS__ROUTEROS__SSL=true \
//	JOCASTA_PLUGINS__ROUTEROS__INSECURE=true \
//	JOCASTA_PLUGINS__ROUTEROS__USER=jocasta \
//	JOCASTA_PLUGINS__ROUTEROS__PASSWORD=... \
//	go test ./internal/plugin -run TestDiscoverLive -v
func TestDiscoverLive(t *testing.T) {
	host := os.Getenv(liveHostEnv)
	if host == "" {
		t.Skipf("set %s to read a real router", liveHostEnv)
	}

	cfg := &routeros.Config{
		Host:     host,
		User:     os.Getenv(liveUserEnv),
		Password: os.Getenv(livePasswordEnv),
		SSL:      liveEnvBool(t, liveSSLEnv),
		Insecure: liveEnvBool(t, liveInsecureEnv),
	}

	if raw := os.Getenv(livePortEnv); raw != "" {
		port, err := strconv.Atoi(raw)
		require.NoError(t, err)

		cfg.Port = port
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	client, err := routeros.New(cfg, log)
	require.NoError(t, err)

	// Verify separately: a router that refuses here refuses everything, and its
	// error says which of reachability, TLS and credentials was the problem.
	res, err := client.Verify(t.Context())
	require.NoError(t, err)

	t.Logf("board=%q version=%q", res.BoardName, res.Version)

	p, err := NewRouterOS("gateway", client, log)
	require.NoError(t, err)

	assert.Equal(t, "routeros:gateway", p.Name())

	// The raw tables, to compare the mapping against.
	arp, err := client.ARP(t.Context())
	require.NoError(t, err)

	leases, err := client.DHCPLeases(t.Context())
	require.NoError(t, err)

	usableARP := 0

	for _, e := range arp {
		if e.Usable() {
			usableARP++
		}
	}

	facts, err := p.Discover(t.Context())
	require.NoError(t, err, "a partial read would report here")

	present, named, withMAC := 0, 0, 0

	for _, f := range facts {
		if f.Present {
			present++
		}

		if f.Host.Hostname() != "" {
			named++
		}

		if f.Host.MAC != "" {
			withMAC++
		}
	}

	t.Logf("%d arp rows (%d usable) + %d leases -> %d facts: %d present, %d named, %d with a MAC",
		len(arp), usableARP, len(leases), len(facts), present, named, withMAC)

	w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
	_, _ = w.Write([]byte("\nADDRESS\tMAC\tVENDOR\tHOSTNAME\tSTANDING\tPRESENT\tDETAIL\n"))

	for _, f := range facts {
		_, _ = w.Write([]byte(liveRow(
			f.Host.IP,
			f.Host.MAC,
			f.Host.ShortName(),
			f.Host.Hostname(),
			string(f.HostnameSource),
			strconv.FormatBool(f.Present),
			liveDetail(f.Detail),
		)))
	}

	_ = w.Flush()

	// The invariants the mapping is responsible for, checked against whatever
	// the router actually holds rather than against a fixture.
	assert.LessOrEqual(t, len(facts), len(arp)+len(leases), "no fact is invented")
	assert.LessOrEqual(t, present, usableARP+len(leases), "presence needs a row that says so")

	for _, f := range facts {
		assert.True(t, f.Host.Address().IsValid(), "every fact carries a parsed address")
		assert.NotEqual(t, zeroMAC, f.Host.MAC, "the zero address identifies nothing")

		// A name and its standing travel together: one without the other means
		// either an unattributed name or a standing for nothing.
		assert.Equal(t, f.Host.Hostname() != "", f.HostnameSource != "",
			"hostname %q and standing %q disagree", f.Host.Hostname(), f.HostnameSource)

		if f.HostnameSource != "" {
			assert.Contains(t,
				[]dbtype.HostnameSource{dbtype.HostnameFromDHCPStatic, dbtype.HostnameFromDHCPLease},
				f.HostnameSource, "a router only ever reports lease names")
		}
	}

	assert.True(t, sortedByAddress(facts), "facts leave sorted by address")
}

func sortedByAddress(facts []Fact) bool {
	return slices.IsSortedFunc(facts, func(a, b Fact) int {
		return a.Host.Address().Compare(b.Host.Address())
	})
}

func liveEnvBool(t *testing.T, key string) bool {
	t.Helper()

	raw := os.Getenv(key)
	if raw == "" {
		return false
	}

	b, err := strconv.ParseBool(raw)
	require.NoError(t, err)

	return b
}

func liveDetail(d map[string]string) string {
	if len(d) == 0 {
		return ""
	}

	out := ""

	for _, k := range []string{"interface", "arp_status", "dhcp_server", "dhcp_status", "dhcp_comment"} {
		if v, ok := d[k]; ok {
			if out != "" {
				out += " "
			}

			out += k + "=" + v
		}
	}

	return out
}

func liveRow(fields ...string) string {
	out := ""

	for i, f := range fields {
		if i > 0 {
			out += "\t"
		}

		if f == "" {
			f = "-"
		}

		out += f
	}

	return out + "\n"
}
