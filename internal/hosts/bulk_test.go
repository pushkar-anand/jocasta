package hosts

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// delayedResolver answers PTR queries from ptr as stubResolver does, but holds
// every dial for delay and counts them, so a test can watch concurrent lookups
// for one address collapse into a single query.
func delayedResolver(t *testing.T, ptr map[string]string, delay time.Duration) (*net.Resolver, *atomic.Int64) {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = pc.Close() })

	go serveDNS(pc, ptr)

	addr := pc.LocalAddr().String()

	var calls atomic.Int64

	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			calls.Add(1)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			var d net.Dialer

			return d.DialContext(ctx, "udp", addr)
		},
	}

	return r, &calls
}

// The ARP and DHCP tables both hold the same address, so a merged sweep asks
// about it more than once. Those lookups run together and share one query.
func TestBulkBuildLooksUpARepeatedAddressOnce(t *testing.T) {
	r, calls := delayedResolver(t, map[string]string{
		"10.2.0.192.in-addr.arpa.": "printer.lan.",
	}, 150*time.Millisecond)
	useResolver(t, r)

	inputs := make([]HostInput, buildConcurrency)
	for i := range inputs {
		inputs[i] = HostInput{IP: "192.0.2.10"}
	}

	out, err := BulkBuild(t.Context(), inputs)
	require.NoError(t, err)
	require.Len(t, out, buildConcurrency)

	for _, h := range out {
		assert.Equal(t, "printer.lan", h.Hostname(), "every caller takes the shared answer")
	}

	assert.Equal(t, int64(1), calls.Load(), "one address in flight, one query")
}

// A router fills these tables in, so a malformed row is ordinary. It costs its
// own entry and leaves the rest of the sweep standing.
func TestBulkBuildKeepsTheHostsThatBuilt(t *testing.T) {
	useResolver(t, stubResolver(t, nil))

	out, err := BulkBuild(t.Context(), []HostInput{
		{IP: "192.0.2.10", MAC: "00:00:0c:11:22:33"},
		{IP: "not-an-address"},
		{IP: "192.0.2.11", MAC: "zz:zz"},
		{IP: "192.0.2.12", MAC: "00:00:0c:44:55:66"},
	})

	require.Error(t, err)
	require.Len(t, out, 2)

	assert.Equal(t, "192.0.2.10", out[0].IP)
	assert.Equal(t, "192.0.2.12", out[1].IP, "the survivors keep their input order")

	assert.ErrorContains(t, err, `"not-an-address"`, "the error names the row it came from")
	assert.ErrorContains(t, err, `"192.0.2.11"`)
}

func TestBulkBuildPreservesInputOrder(t *testing.T) {
	useResolver(t, stubResolver(t, nil))

	const total = buildConcurrency * 3

	inputs := make([]HostInput, total)
	for i := range inputs {
		inputs[i] = HostInput{IP: fmt.Sprintf("192.0.2.%d", i)}
	}

	out, err := BulkBuild(t.Context(), inputs)
	require.NoError(t, err)
	require.Len(t, out, total)

	for i, h := range out {
		assert.Equal(t, inputs[i].IP, h.IP)
	}
}

func TestBulkBuildWithNoInputs(t *testing.T) {
	t.Parallel()

	out, err := BulkBuild(t.Context(), nil)

	require.NoError(t, err)
	assert.Empty(t, out)
}

// A cancelled sweep is short, not complete, and must say so: a caller that saw
// only the error would otherwise read a truncated table as the whole one.
func TestBulkBuildReportsACancelledSweep(t *testing.T) {
	useResolver(t, stubResolver(t, nil))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	out, err := BulkBuild(ctx, []HostInput{{IP: "192.0.2.10"}, {IP: "192.0.2.11"}})

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, out)
}
