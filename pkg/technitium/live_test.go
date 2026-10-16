package technitium

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This test reads the environment that koanf binds to a technitium instance
// named "resolver". So a shell that configures jocasta can run this test
// without more variables.
const (
	hostEnv     = "JOCASTA_PLUGINS__TECHNITIUM__RESOLVER__HOST"
	portEnv     = "JOCASTA_PLUGINS__TECHNITIUM__RESOLVER__PORT"
	tokenEnv    = "JOCASTA_PLUGINS__TECHNITIUM__RESOLVER__TOKEN" //nolint:gosec // the name of an environment variable.
	sslEnv      = "JOCASTA_PLUGINS__TECHNITIUM__RESOLVER__SSL"
	insecureEnv = "JOCASTA_PLUGINS__TECHNITIUM__RESOLVER__INSECURE"
)

// liveClient builds a client for the server that the environment names. If
// the environment names no server, it skips the test. The rest of the suite
// must stay hermetic.
func liveClient(t *testing.T) *Technitium {
	t.Helper()

	host := os.Getenv(hostEnv)
	if host == "" {
		t.Skipf("set %s to read a real server", hostEnv)
	}

	cfg := &Config{
		Host:     host,
		Token:    os.Getenv(tokenEnv),
		SSL:      envBool(t, sslEnv),
		Insecure: envBool(t, insecureEnv),
	}

	if raw := os.Getenv(portEnv); raw != "" {
		port, err := strconv.Atoi(raw)
		require.NoError(t, err)

		cfg.Port = port
	}

	c, err := New(cfg)
	require.NoError(t, err)

	return c
}

func envBool(t *testing.T, name string) bool {
	t.Helper()

	raw := os.Getenv(name)
	if raw == "" {
		return false
	}

	v, err := strconv.ParseBool(raw)
	require.NoError(t, err, name)

	return v
}

// TestReadLive reads a real server and prints the result. In a cluster, run
// it once for each node, with HOST set to the address of that node. Each node
// keeps its own log. The primary copies the token and log app to each node.
//
//	JOCASTA_PLUGINS__TECHNITIUM__RESOLVER__HOST=192.0.2.53 \
//	JOCASTA_PLUGINS__TECHNITIUM__RESOLVER__TOKEN=... \
//	go test ./pkg/technitium -run TestReadLive -v
func TestReadLive(t *testing.T) {
	c := liveClient(t)
	ctx := t.Context()

	apps, err := c.Apps(ctx)
	require.NoError(t, err)

	var app, classPath string

	for _, a := range apps {
		for _, d := range a.DNSApps {
			t.Logf("app %q class %q query logger %t", a.Name, d.ClassPath, d.IsQueryLogger)

			if d.IsQueryLogger && app == "" {
				app, classPath = a.Name, d.ClassPath
			}
		}
	}

	if app == "" {
		t.Skip("no installed app logs queries")
	}

	page, err := c.QueryLogs(ctx, LogQuery{
		App:       app,
		ClassPath: classPath,
		Start:     time.Now().Add(-10 * time.Minute),
		PerPage:   20,
	})
	require.NoError(t, err)

	t.Logf("page %d of %d, %d entries in all", page.PageNumber, page.TotalPages, page.TotalEntries)

	for _, e := range page.Entries {
		t.Logf("%s %s %s %s %s %s %q", e.Timestamp, e.ClientIPAddress, e.QType, e.QName, e.ResponseType, e.RCode, e.Answer)
	}

	if len(page.Entries) == 0 {
		t.Skip("nothing logged in the last 10 minutes")
	}

	// If start is equal to the timestamp of an entry, sent in UTC, the server
	// returns that entry first. The server reads start as UTC and inclusive,
	// to the 100ns that it stores.
	first := page.Entries[0]

	at, err := time.Parse(time.RFC3339Nano, first.Timestamp)
	require.NoError(t, err, "timestamp %q", first.Timestamp)

	again, err := c.QueryLogs(ctx, LogQuery{App: app, ClassPath: classPath, Start: at, PerPage: 1})
	require.NoError(t, err)
	require.NotEmpty(t, again.Entries)
	assert.Equal(t, first, again.Entries[0])
}
