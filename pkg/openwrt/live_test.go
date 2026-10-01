package openwrt

import (
	"log/slog"
	"maps"
	"os"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// The environment this test reads is the one koanf binds an openwrt instance
// named "gateway" to, so a shell that configures jocasta can run this without
// restating anything.
const (
	hostEnv     = "JOCASTA_PLUGINS__OPENWRT__GATEWAY__HOST"
	portEnv     = "JOCASTA_PLUGINS__OPENWRT__GATEWAY__PORT"
	userEnv     = "JOCASTA_PLUGINS__OPENWRT__GATEWAY__USER"
	passwordEnv = "JOCASTA_PLUGINS__OPENWRT__GATEWAY__PASSWORD" //nolint:gosec // the name of an environment variable.
	sslEnv      = "JOCASTA_PLUGINS__OPENWRT__GATEWAY__SSL"
	insecureEnv = "JOCASTA_PLUGINS__OPENWRT__GATEWAY__INSECURE"
)

// liveClient builds a client for the router the environment names, and skips
// the test when it names none: the rest of the suite must stay hermetic.
func liveClient(t *testing.T) *OpenWrt {
	t.Helper()

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

	o, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	require.NoError(t, err)

	return o
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

// TestReadLive reads a real router and prints what came back. It exists to
// produce field data to design the mapping against, so it asserts almost
// nothing beyond the reads succeeding.
//
//	JOCASTA_PLUGINS__OPENWRT__GATEWAY__HOST=192.0.2.1 \
//	JOCASTA_PLUGINS__OPENWRT__GATEWAY__USER=jocasta \
//	JOCASTA_PLUGINS__OPENWRT__GATEWAY__PASSWORD=... \
//	go test ./pkg/openwrt -run TestReadLive -v
func TestReadLive(t *testing.T) {
	o := liveClient(t)

	board, err := o.Verify(t.Context())
	require.NoError(t, err)

	t.Logf("hostname=%q model=%q release=%q", board.Hostname, board.Model, board.Release.Description)

	neigh, err := o.Neighbours(t.Context())
	require.NoError(t, err)

	for _, n := range neigh {
		t.Logf("neighbour %-40s %-12s %-17s %s", n.Address, n.Device, n.MAC, n.State)
	}

	leases, err := o.DHCPLeases(t.Context())
	require.NoError(t, err)

	for _, l := range leases {
		t.Logf("lease %-40s %-17s %q ipv6=%t", l.Address, l.MAC, l.Hostname, l.IPv6)
	}

	static, err := o.StaticHosts(t.Context())
	require.NoError(t, err)

	for _, h := range static {
		t.Logf("static %q %v %q", h.Name, h.MACs, h.Address)
	}

	ifaces, err := o.Interfaces(t.Context())
	require.NoError(t, err)

	for _, i := range ifaces {
		t.Logf("interface %-10s up=%t proto=%-7s device=%-8s v4=%v v6=%v assigned=%v",
			i.Name, i.Up, i.Proto, i.L3Device, i.IPv4, i.IPv6, i.Assigned)
	}

	vlans, err := o.VLANs(t.Context())
	require.NoError(t, err)

	t.Logf("vlans %v", vlans)

	devices, err := o.NetworkDevices(t.Context())
	require.NoError(t, err)

	for _, name := range slices.Sorted(maps.Keys(devices)) {
		d := devices[name]
		t.Logf("device %-10s devtype=%-8s port=%t bridge=%t members=%v mac=%s up=%t carrier=%t speed=%d %s",
			name, d.DevType, d.IsPort(), d.Bridge, d.Ports, d.MAC, d.Up, d.Link.Carrier, d.Link.Speed, d.Link.Duplex)

		if d.Bridge {
			fdb, err := o.BridgeFDB(t.Context(), name, d.Ports)
			require.NoError(t, err)

			for _, e := range fdb {
				t.Logf("  fdb %s port=%q local=%t", e.MAC, e.Port, e.Local)
			}
		}
	}

	bridged, err := o.BridgeVLANs(t.Context())
	require.NoError(t, err)

	t.Logf("bridge vlans %v", bridged)

	radios, err := o.WirelessDevices(t.Context())
	require.NoError(t, err)

	for _, r := range radios {
		t.Logf("radio %s up=%t band=%q", r.Name, r.Up, r.Band)

		for _, i := range r.Interfaces {
			if i.Ifname == "" {
				continue
			}

			st, err := o.Stations(t.Context(), i.Ifname)
			require.NoError(t, err)

			t.Logf("  %s %q %s: %d clients", i.Ifname, i.SSID, i.Mode, len(st))
		}
	}
}
