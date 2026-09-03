package scanner

import (
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestParsePortSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec string
		want []uint16
		err  string
	}{
		{name: "single port", spec: "22", want: []uint16{22}},
		{name: "list", spec: "22,80,443", want: []uint16{22, 80, 443}},
		{name: "range", spec: "80-82", want: []uint16{80, 81, 82}},
		{name: "list and range, unordered", spec: "443,80-82", want: []uint16{80, 81, 82, 443}},
		{name: "overlap is deduplicated", spec: "80-82,81,82-83", want: []uint16{80, 81, 82, 83}},
		{name: "whitespace is ignored", spec: " 22 , 80 ", want: []uint16{22, 80}},
		{name: "single-port range", spec: "22-22", want: []uint16{22}},
		{name: "empty spec", spec: "", err: "empty entry"},
		{name: "empty entry", spec: "22,,80", err: "empty entry"},
		{name: "trailing comma", spec: "22,", err: "empty entry"},
		{name: "not a number", spec: "http", err: "not a number"},
		{name: "port zero", spec: "0", err: "out of range"},
		{name: "port above 65535", spec: "70000", err: "out of range"},
		{name: "range end out of bounds", spec: "80-70000", err: "out of range"},
		{name: "reversed range", spec: "100-50", err: "ends before it starts"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePortSpec(tc.spec)

			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// A full scan is asked for as "1-65535", so that spec has to enumerate the
// whole range without the p++ on 65535 wrapping to zero.
func TestParsePortSpecFullRange(t *testing.T) {
	t.Parallel()

	got, err := ParsePortSpec("1-65535")
	require.NoError(t, err)

	require.Len(t, got, 65535)
	assert.Equal(t, uint16(1), got[0])
	assert.Equal(t, uint16(65535), got[len(got)-1])
}

func TestServiceName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "ssh", ServiceName(22))
	assert.Equal(t, "https", ServiceName(443))
	assert.Empty(t, ServiceName(12345), "a port with no well-known service names none")
}

// presetPorts is derived from the service map's keys, so it should carry one
// entry per service, ascending, with the zero port impossible.
func TestPresetPortsDerivedFromServices(t *testing.T) {
	t.Parallel()

	assert.Len(t, presetPorts, len(presetServices))
	assert.True(t, slices.IsSorted(presetPorts), "preset is not ascending")
	assert.NotContains(t, presetPorts, uint16(0), "preset contains the zero port")

	for _, p := range presetPorts {
		assert.NotEmptyf(t, ServiceName(p), "preset port %d has no service name", p)
	}
}

func TestWithPortsNormalises(t *testing.T) {
	t.Parallel()

	ps := NewPortScanner(discardLogger(), WithPorts([]uint16{443, 22, 443, 0, 80}))

	assert.Equal(t, []uint16{22, 80, 443}, ps.Ports())
}

func TestWithPortsEmptyKeepsPreset(t *testing.T) {
	t.Parallel()

	ps := NewPortScanner(discardLogger(), WithPorts(nil))

	assert.Equal(t, presetPorts, ps.Ports())
}

func TestPortScannerFindsOpenPorts(t *testing.T) {
	t.Parallel()

	// One port with a listener, one that had one and lost it, so the second is
	// a genuine connection refusal rather than a timeout.
	open := loopback(t)

	gone := loopback(t)
	goneAddr := gone.Addr().(*net.TCPAddr)
	require.NoError(t, gone.Close())

	openPort := uint16(open.Addr().(*net.TCPAddr).Port) //nolint:gosec // a kernel-assigned port fits.
	closedPort := uint16(goneAddr.Port)                 //nolint:gosec // as above.

	ps := NewPortScanner(discardLogger(),
		WithPorts([]uint16{openPort, closedPort}),
		WithDialTimeout(time.Second),
	)

	at := time.Now()
	got := ps.Scan(t.Context(), []netip.Addr{netip.MustParseAddr("127.0.0.1")}, at)

	require.Len(t, got, 1)
	assert.Equal(t, []uint16{openPort}, got[0].Open)
	assert.Len(t, got[0].Scanned, 2)
	assert.Equal(t, at, got[0].SeenAt)
}

// Every target comes back in the order it was given, even one with nothing
// open, because ingest reads the scanned set off each result.
func TestPortScannerReturnsEveryTargetInOrder(t *testing.T) {
	t.Parallel()

	l := loopback(t)
	port := uint16(l.Addr().(*net.TCPAddr).Port) //nolint:gosec // kernel-assigned.

	targets := []netip.Addr{
		netip.MustParseAddr("127.0.0.2"),
		netip.MustParseAddr("127.0.0.1"),
	}

	ps := NewPortScanner(discardLogger(), WithPorts([]uint16{port}), WithDialTimeout(time.Second))
	got := ps.Scan(t.Context(), targets, time.Now())

	require.Len(t, got, 2)
	assert.Equal(t, targets[0], got[0].Addr)
	assert.Equal(t, targets[1], got[1].Addr)
	assert.Empty(t, got[0].Open, "127.0.0.2 has no listener")
	assert.Equal(t, []uint16{port}, got[1].Open)
}

func TestPortScannerHandlesNoTargets(t *testing.T) {
	t.Parallel()

	ps := NewPortScanner(discardLogger())

	assert.Empty(t, ps.Scan(t.Context(), nil, time.Now()))
}

// loopback opens a listener bound to a free loopback port and closes it when
// the test ends.
func loopback(t *testing.T) net.Listener {
	t.Helper()

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = l.Close() })

	return l
}
