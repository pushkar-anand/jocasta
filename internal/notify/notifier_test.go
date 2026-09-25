package notify_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/notify"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// wait is how long a test waits for the notifier's goroutine to send.
const wait = 5 * time.Second

// inbox is an ntfy server that hands each published message to the test.
func inbox(t *testing.T, status int) (*httptest.Server, chan map[string]any) {
	t.Helper()

	got := make(chan map[string]any, 8)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		got <- body

		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	return srv, got
}

// running starts a notifier over a fresh store, listening for its scans.
func running(t *testing.T, ds ...*notify.Destination) (*notify.Notifier, *inventory.Store) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	log := slog.New(slog.DiscardHandler)
	store := inventory.New(conn, log)
	n := notify.New(conn, store, log, ds...)
	store.OnScanFinished(n.Queue)

	go func() { _ = n.Run(t.Context()) }()

	return n, store
}

// sweepHosts records a sweep of 192.0.2.0/24 that found each address, with
// the MAC of its position in the list, so the same address in a later sweep
// is the same device.
func sweepHosts(t *testing.T, store *inventory.Store, ips ...string) {
	t.Helper()

	found := make([]scanner.Host, 0, len(ips))

	for i, ip := range ips {
		h, err := hosts.BuildHost(t.Context(), hosts.HostInput{
			IP: ip, MAC: "00:00:5e:00:53:" + []string{"01", "02", "03", "04"}[i],
		})
		require.NoError(t, err)

		found = append(found, scanner.Host{Host: h})
	}

	_, err := store.RecordSweep(t.Context(), "sweep", netip.MustParsePrefix("192.0.2.0/24"), found)
	require.NoError(t, err)
}

func receive(t *testing.T, got chan map[string]any) map[string]any {
	t.Helper()

	select {
	case body := <-got:
		return body
	case <-time.After(wait):
		t.Fatal("no message was sent")
		return nil
	}
}

func TestNotifierSendsEachScanToTheKindsChosen(t *testing.T) {
	t.Parallel()

	srv, got := inbox(t, http.StatusOK)
	phone, err := notify.NewDestination("phone", notify.Config{Ntfy: &notify.Ntfy{URL: srv.URL + "/jocasta"}})
	require.NoError(t, err)

	n, store := running(t, phone)
	require.NoError(t, n.SetRules(t.Context(), "phone", []dbtype.EventKind{dbtype.EventDeviceDiscovered}))

	sweepHosts(t, store, "192.0.2.10")
	assert.Equal(t, "First scan on 192.0.2.0/24", receive(t, got)["title"])

	sweepHosts(t, store, "192.0.2.10", "192.0.2.11")
	body := receive(t, got)
	assert.Equal(t, "1 new device on 192.0.2.0/24", body["title"])

	last, ok := n.Last("phone")
	require.True(t, ok)
	assert.NoError(t, last.Err)

	// A sweep that changes nothing sends nothing.
	sweepHosts(t, store, "192.0.2.10", "192.0.2.11")

	select {
	case body := <-got:
		t.Fatalf("sent %v for a scan that changed nothing", body)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNotifierSendsNothingWithoutKinds(t *testing.T) {
	t.Parallel()

	srv, got := inbox(t, http.StatusOK)
	phone, err := notify.NewDestination("phone", notify.Config{Ntfy: &notify.Ntfy{URL: srv.URL + "/jocasta"}})
	require.NoError(t, err)

	_, store := running(t, phone)
	sweepHosts(t, store, "192.0.2.10")

	select {
	case body := <-got:
		t.Fatalf("sent %v with no kinds chosen", body)
	case <-time.After(200 * time.Millisecond):
	}
}

// One destination failing is recorded, and the others are still sent.
func TestNotifierRecordsAFailedSend(t *testing.T) {
	t.Parallel()

	downSrv, downGot := inbox(t, http.StatusUnauthorized)
	upSrv, upGot := inbox(t, http.StatusOK)

	down, err := notify.NewDestination("down", notify.Config{Ntfy: &notify.Ntfy{URL: downSrv.URL + "/jocasta"}})
	require.NoError(t, err)

	up, err := notify.NewDestination("up", notify.Config{Ntfy: &notify.Ntfy{URL: upSrv.URL + "/jocasta"}})
	require.NoError(t, err)

	n, store := running(t, down, up)

	for _, name := range []string{"down", "up"} {
		require.NoError(t, n.SetRules(t.Context(), name, []dbtype.EventKind{dbtype.EventDeviceDiscovered}))
	}

	sweepHosts(t, store, "192.0.2.10")
	receive(t, downGot)
	receive(t, upGot)

	require.Eventually(t, func() bool {
		last, ok := n.Last("down")
		return ok && last.Err != nil
	}, wait, 10*time.Millisecond)
}

func TestSendTestIsRecorded(t *testing.T) {
	t.Parallel()

	srv, got := inbox(t, http.StatusOK)
	phone, err := notify.NewDestination("phone", notify.Config{Ntfy: &notify.Ntfy{URL: srv.URL + "/jocasta"}})
	require.NoError(t, err)

	n, _ := running(t, phone)

	d, ok := n.Destination("phone")
	require.True(t, ok)
	require.NoError(t, n.SendTest(t.Context(), d))
	assert.Equal(t, "Jocasta test", receive(t, got)["title"])

	last, ok := n.Last("phone")
	require.True(t, ok)
	assert.NoError(t, last.Err)

	_, ok = n.Destination("tablet")
	assert.False(t, ok)
}

func TestRules(t *testing.T) {
	t.Parallel()

	n, _ := running(t)
	ctx := t.Context()

	kinds, err := n.Rules(ctx, "phone")
	require.NoError(t, err)
	assert.Empty(t, kinds)

	want := []dbtype.EventKind{dbtype.EventDeviceDiscovered, dbtype.EventPortOpened}
	require.NoError(t, n.SetRules(ctx, "phone", want))

	kinds, err = n.Rules(ctx, "phone")
	require.NoError(t, err)
	assert.ElementsMatch(t, want, kinds)

	kinds, err = n.Rules(ctx, "hook")
	require.NoError(t, err)
	assert.Empty(t, kinds, "rules are per destination")

	require.NoError(t, n.SetRules(ctx, "phone", nil))

	kinds, err = n.Rules(ctx, "phone")
	require.NoError(t, err)
	assert.Empty(t, kinds, "none clears them")
}
