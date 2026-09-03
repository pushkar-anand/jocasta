package scanner

import (
	"cmp"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/stretchr/testify/require"
)

// portsEnv holds one or more comma-separated addresses to port-scan. The test
// is skipped without it: the rest of the suite stays hermetic, and this one
// opens connections to real hosts.
const portsEnv = "JOCASTA_PORTS_TARGET"

// TestPortScanLive scans real addresses with the preset and prints what
// answered. It exists to produce field data to tune the preset against, so it
// asserts almost nothing.
//
//	JOCASTA_PORTS_TARGET=192.0.2.10,192.0.2.20 go test ./internal/scanner -run TestPortScanLive -v
func TestPortScanLive(t *testing.T) {
	raw := os.Getenv(portsEnv)
	if raw == "" {
		t.Skipf("set %s to port-scan a real address", portsEnv)
	}

	var targets []netip.Addr

	for field := range strings.SplitSeq(raw, ",") {
		addr, err := netip.ParseAddr(strings.TrimSpace(field))
		require.NoError(t, err)

		targets = append(targets, addr)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ps := NewPortScanner(log)

	start := time.Now()
	results := ps.Scan(t.Context(), targets, start)

	t.Logf("scanned %d ports on %d addresses in %s",
		len(ps.Ports()), len(targets), time.Since(start).Round(time.Millisecond))

	w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
	_, _ = w.Write([]byte("\nADDRESS\tPORT\tSERVICE\n"))

	for _, r := range results {
		for _, port := range r.Open {
			_, _ = w.Write([]byte(r.Addr.String() + "\t" +
				strconv.Itoa(int(port)) + "\t" + cmp.Or(ServiceName(port), "-") + "\n"))
		}
	}

	_ = w.Flush()
}
