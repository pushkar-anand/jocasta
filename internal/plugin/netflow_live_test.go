package plugin

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The environment that koanf binds to a netflow instance named "gateway",
// with the exporters comma-separated.
const (
	liveFlowListenEnv    = "JOCASTA_PLUGINS__NETFLOW__GATEWAY__LISTEN"
	liveFlowExportersEnv = "JOCASTA_PLUGINS__NETFLOW__GATEWAY__EXPORTERS"

	// liveFlowWaitEnv limits how long the test waits for a first flow, as a
	// duration. An exporter sends a flow only when it times out. So this is
	// longer than the shortest timeout of the exporter.
	liveFlowWaitEnv = "JOCASTA_NETFLOW_LIVE_WAIT"
)

// TestNetFlowLive listens for a real exporter. It prints every flow decoded in
// the first batch that has one, and in each batch within a second of it. It
// asserts only that a flow arrives.
//
// Without a listen address, it skips. The rest of the suite must stay
// hermetic.
//
//	JOCASTA_PLUGINS__NETFLOW__GATEWAY__LISTEN=:2055 \
//	JOCASTA_PLUGINS__NETFLOW__GATEWAY__EXPORTERS=192.0.2.1 \
//	go test ./internal/plugin -run TestNetFlowLive -v
func TestNetFlowLive(t *testing.T) {
	listen := os.Getenv(liveFlowListenEnv)
	if listen == "" {
		t.Skipf("set %s to listen for a real exporter", liveFlowListenEnv)
	}

	wait := 2 * time.Minute

	if raw := os.Getenv(liveFlowWaitEnv); raw != "" {
		d, err := time.ParseDuration(raw)
		require.NoError(t, err, liveFlowWaitEnv)

		wait = d
	}

	n, err := NewNetFlow("gateway", listen, strings.Split(os.Getenv(liveFlowExportersEnv), ","),
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()

	var (
		mu    sync.Mutex
		flows []Flow
		once  sync.Once
	)

	done := make(chan error, 1)

	go func() {
		done <- n.Listen(ctx, func(_ context.Context, batch []Flow) {
			mu.Lock()
			defer mu.Unlock()

			flows = append(flows, batch...)

			if len(flows) > 0 {
				// Batches that an exporter sends together arrive within moments
				// of each other. So one more second collects the rest.
				once.Do(func() { time.AfterFunc(time.Second, cancel) })
			}
		})
	}()

	require.NoError(t, <-done)

	mu.Lock()
	defer mu.Unlock()

	require.NotEmpty(t, flows, "no flow arrived within %s", wait)

	for _, f := range flows {
		t.Logf("flow %s:%d -> %s:%d proto=%d bytes=%d packets=%d exporter=%s",
			f.Src, f.SrcPort, f.Dst, f.DstPort, f.Protocol, f.Bytes, f.Packets, f.Exporter)
	}
}
