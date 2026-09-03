package routeros

import (
	"log/slog"
	"os"
	"strconv"
	"testing"
	"text/tabwriter"

	"github.com/stretchr/testify/require"
)

// The environment this test reads is the one koanf already binds the running
// application's routeros settings to, so a shell that can configure jocasta
// can run this without restating anything.
const (
	hostEnv     = "JOCASTA_PLUGINS__ROUTEROS__HOST"
	portEnv     = "JOCASTA_PLUGINS__ROUTEROS__PORT"
	userEnv     = "JOCASTA_PLUGINS__ROUTEROS__USER"
	passwordEnv = "JOCASTA_PLUGINS__ROUTEROS__PASSWORD" //nolint:gosec // the name of a variable, not a credential.
	sslEnv      = "JOCASTA_PLUGINS__ROUTEROS__SSL"
	insecureEnv = "JOCASTA_PLUGINS__ROUTEROS__INSECURE"
)

// TestReadLive reads a real router and prints what came back. It exists to
// produce field data to design the mapping against, so it asserts almost
// nothing beyond the reads succeeding.
//
// It is skipped without a host: the rest of the suite must stay hermetic, and
// this one talks to hardware.
//
//	JOCASTA_PLUGINS__ROUTEROS__HOST=192.0.2.1 \
//	JOCASTA_PLUGINS__ROUTEROS__SSL=true \
//	JOCASTA_PLUGINS__ROUTEROS__INSECURE=true \
//	JOCASTA_PLUGINS__ROUTEROS__USER=jocasta \
//	JOCASTA_PLUGINS__ROUTEROS__PASSWORD=... \
//	go test ./pkg/routeros -run TestReadLive -v
func TestReadLive(t *testing.T) {
	host := os.Getenv(hostEnv)
	if host == "" {
		t.Skipf("set %s to read a real router", hostEnv)
	}

	cfg := &Config{
		Host:     host,
		User:     os.Getenv(userEnv),
		Password: os.Getenv(passwordEnv),
		SSL:      envBool(t, sslEnv),
		Insecure: envBool(t, insecureEnv),
	}

	if raw := os.Getenv(portEnv); raw != "" {
		port, err := strconv.Atoi(raw)
		require.NoError(t, err)

		cfg.Port = port
	}

	r, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	require.NoError(t, err)

	t.Logf("reading %s", r.Addr())

	// Verify first and separately: a router that refuses here refuses
	// everything, and its error says which of reachability, TLS and
	// credentials was the problem.
	res, err := r.Verify(t.Context())
	require.NoError(t, err)

	t.Logf("board=%q version=%q platform=%q arch=%q uptime=%q",
		res.BoardName, res.Version, res.Platform, res.ArchitectureName, res.Uptime)

	t.Run("arp", func(t *testing.T) {
		entries, err := r.ARP(t.Context())
		require.NoError(t, err)

		usable := 0
		ifaces := map[string]int{}

		for _, e := range entries {
			if e.Usable() {
				usable++
			}

			ifaces[e.Interface]++
		}

		t.Logf("%d entries, %d usable, across %d interfaces %v", len(entries), usable, len(ifaces), ifaces)

		w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
		_, _ = w.Write([]byte("\nADDRESS\tMAC\tIFACE\tCOMPLETE\tDYNAMIC\tINVALID\tSTATUS\tCOMMENT\n"))

		for _, e := range entries {
			_, _ = w.Write([]byte(row(e.Address, e.MACAddress, e.Interface,
				strconv.FormatBool(bool(e.Complete)), strconv.FormatBool(bool(e.Dynamic)),
				strconv.FormatBool(bool(e.Invalid)), e.Status, e.Comment)))
		}

		_ = w.Flush()
	})

	t.Run("leases", func(t *testing.T) {
		leases, err := r.DHCPLeases(t.Context())
		require.NoError(t, err)

		var static, bound, named, commented int

		for _, l := range leases {
			if l.Static() {
				static++
			}

			if l.Bound() {
				bound++
			}

			if l.HostName != "" {
				named++
			}

			if l.Comment != "" {
				commented++
			}
		}

		t.Logf("%d leases, %d static, %d bound, %d with a host-name, %d with a comment",
			len(leases), static, bound, named, commented)

		w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
		_, _ = w.Write([]byte("\nADDRESS\tMAC\tHOST-NAME\tCOMMENT\tSERVER\tSTATUS\tDYNAMIC\tEXPIRES\tLAST-SEEN\n"))

		for _, l := range leases {
			_, _ = w.Write([]byte(row(l.Address, l.MACAddress, l.HostName, l.Comment, l.Server,
				l.Status, strconv.FormatBool(bool(l.Dynamic)), l.ExpiresAfter, l.LastSeen)))
		}

		_ = w.Flush()
	})

	t.Run("addresses", func(t *testing.T) {
		addrs, err := r.Addresses(t.Context())
		require.NoError(t, err)

		usable := 0

		for _, a := range addrs {
			if a.Usable() {
				usable++
			}
		}

		t.Logf("%d addresses, %d naming a segment the router serves", len(addrs), usable)

		w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
		_, _ = w.Write([]byte("\nADDRESS\tNETWORK\tIFACE\tACTUAL\tDYNAMIC\tDISABLED\tINVALID\tCOMMENT\n"))

		for _, a := range addrs {
			_, _ = w.Write([]byte(row(a.Address, a.Network, a.Interface, a.ActualInterface,
				strconv.FormatBool(bool(a.Dynamic)), strconv.FormatBool(bool(a.Disabled)),
				strconv.FormatBool(bool(a.Invalid)), a.Comment)))
		}

		_ = w.Flush()
	})

	t.Run("vlans", func(t *testing.T) {
		vlans, err := r.VLANs(t.Context())
		require.NoError(t, err)

		tagged := 0

		for _, v := range vlans {
			if _, ok := v.Tag(); ok {
				tagged++
			}
		}

		t.Logf("%d vlans, %d with a tag that reads as a number", len(vlans), tagged)

		w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
		_, _ = w.Write([]byte("\nNAME\tVLAN-ID\tPARENT\tDISABLED\tRUNNING\tCOMMENT\n"))

		for _, v := range vlans {
			_, _ = w.Write([]byte(row(v.Name, v.VLANID, v.Interface,
				strconv.FormatBool(bool(v.Disabled)), strconv.FormatBool(bool(v.Running)), v.Comment)))
		}

		_ = w.Flush()
	})
}

func envBool(t *testing.T, key string) bool {
	t.Helper()

	raw := os.Getenv(key)
	if raw == "" {
		return false
	}

	b, err := strconv.ParseBool(raw)
	require.NoError(t, err)

	return b
}

// row renders one tab-separated line, showing an empty field as a dash so a
// column that is blank everywhere is visible as such.
func row(fields ...string) string {
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
