package scanner

import (
	"cmp"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/stretchr/testify/require"
)

// cidrEnv holds one or more comma-separated prefixes to sweep. The test is
// skipped without it: the rest of the suite must stay hermetic, and this one
// puts packets on a real network.
const cidrEnv = "JOCASTA_SCAN_CIDR"

// TestSweepLive scans real networks and prints what came back. It exists to
// produce field data to design against, so it asserts almost nothing.
//
//	JOCASTA_SCAN_CIDR=192.0.2.0/24,198.51.100.0/24 go test ./internal/scanner -run TestSweepLive -v
func TestSweepLive(t *testing.T) {
	raw := os.Getenv(cidrEnv)
	if raw == "" {
		t.Skipf("set %s to sweep a real network", cidrEnv)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := New(log)

	for field := range strings.SplitSeq(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(field))
		require.NoError(t, err)

		t.Run(prefix.String(), func(t *testing.T) {
			start := time.Now()

			hosts, err := s.Scan(t.Context(), prefix)
			require.NoError(t, err)

			var withMAC, withName int

			for _, h := range hosts {
				if h.MAC != "" {
					withMAC++
				}

				if h.Hostname != "" {
					withName++
				}
			}

			t.Logf("%s: %d up in %s (%d with MAC, %d with hostname)",
				prefix, len(hosts), time.Since(start).Round(time.Millisecond), withMAC, withName)

			w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
			_, _ = w.Write([]byte("\nIP\tMAC\tVENDOR\tIFACE\tHOSTNAME\tRTT\n"))

			for _, h := range hosts {
				iface := cmp.Or(h.Interface, "-")
				if h.Self {
					iface += " (self)"
				}

				vendor := cmp.Or(h.Vendor, "-")
				if h.Randomised {
					vendor = "(randomised)"
				}

				_, _ = w.Write([]byte(strings.Join([]string{
					h.Addr.String(),
					cmp.Or(h.MAC, "-"),
					vendor,
					iface,
					cmp.Or(h.Hostname, "-"),
					h.RTT.Round(time.Microsecond).String(),
				}, "\t") + "\n"))
			}

			_ = w.Flush()
		})
	}
}
