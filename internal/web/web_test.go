package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/api"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Addresses come from RFC 5737, and hardware addresses from RFC 7042. Both are
// for documentation. So nothing here names a real device.
const (
	prefix = "192.0.2.0/24"

	macA = "00:00:5e:00:53:01"
	macB = "00:00:5e:00:53:02"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testStore opens an inventory over a migrated database for the test only.
func testStore(t *testing.T) *inventory.Store {
	t.Helper()

	store, _ := testStoreWithConn(t)

	return store
}

// testStoreWithConn also returns the connection, for a test that must set up a
// state that the writes of the store cannot reach.
func testStoreWithConn(t *testing.T) (*inventory.Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return inventory.New(conn, testLogger()), conn
}

// testReader builds the request reader of the server.
func testReader(t *testing.T) *request.Reader {
	t.Helper()

	// deviceclass is the only custom tag of the form and query structs in this
	// package. main.go registers the same rule for the real server.
	v, err := validator.New(validator.WithCustomTags(map[string]validator.ValidationFunc{
		"deviceclass": api.DeviceClassRule,
	}))
	require.NoError(t, err)

	return request.NewReader(testLogger(), v)
}

// testUsername and testPassword name the one account in every test Auth. So a
// test that needs a signed-in view does not need its own credential.
const (
	testUsername = "jocasta-test"
	testPassword = "test-password-1"
)

// testAuth builds an Auth over its own migrated database, separate from the
// store of a test. It has the one account for test sign-ins.
func testAuth(t *testing.T) *auth.Auth {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "auth.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	q := models.New(conn)

	hash, err := password.NewHasher().Hash(testPassword)
	require.NoError(t, err)

	_, err = q.CreateUser(t.Context(), models.CreateUserParams{
		Username:     testUsername,
		PasswordHash: hash,
		Role:         dbtype.RoleAdmin,
	})
	require.NoError(t, err)

	a, err := auth.New(conn, q, password.NewHasher())
	require.NoError(t, err)

	return a
}

// unseededAuth is testAuth without an account, for a test of the setup flow
// itself. The setup flow has a meaning only before an account exists.
func unseededAuth(t *testing.T) *auth.Auth {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "auth.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	a, err := auth.New(conn, models.New(conn), password.NewHasher())
	require.NoError(t, err)

	return a
}

// newWebHandler builds the web handler as the server does. It has an HTML
// writer with the error pages, and the templates from NewHandler. It wraps
// them in the same session load-and-save as the real server. Without it, any
// code that uses the session panics, because the context of the request has
// no session data.
func newWebHandler(t *testing.T, store *inventory.Store) http.Handler {
	t.Helper()

	return newWebHandlerWithAuth(t, store, testAuth(t))
}

// newWebHandlerWithAuth is newWebHandler for a test that needs its own handle
// on the Auth of its sign-in. An example is a test that reads the id of a user
// directly.
func newWebHandlerWithAuth(t *testing.T, store *inventory.Store, a *auth.Auth, opts ...Option) http.Handler {
	t.Helper()

	return newWebHandlerWithSession(t, store, a, auth.NewSession(testLogger()), opts...)
}

// newWebHandlerWithSession is newWebHandlerWithAuth over a session manager that
// the test built. An example is a manager with a database that the test reads
// after.
func newWebHandlerWithSession(t *testing.T, store *inventory.Store, a *auth.Auth, sm *auth.Session, opts ...Option) http.Handler {
	t.Helper()

	hw := response.NewHTMLWriter(testLogger(), nil,
		response.WithErrorTemplates(map[int]string{
			http.StatusBadRequest:            TemplateBadRequest,
			http.StatusRequestEntityTooLarge: TemplateBadRequest,
			http.StatusUnprocessableEntity:   TemplateBadRequest,
			http.StatusNotFound:              TemplateNotFound,
			http.StatusUnauthorized:          TemplateLogin,
			http.StatusPreconditionRequired:  TemplateTOTP,
			http.StatusTooManyRequests:       TemplateLogin,
			http.StatusConflict:              TemplateSetup,
			http.StatusForbidden:             TemplateForbidden,
		}),
		response.WithErrorStatusMapper(func(err error) int {
			switch {
			case errors.Is(err, inventory.ErrNotFound), errors.Is(err, auth.ErrNoTOTPEnrollment):
				return http.StatusNotFound
			case errors.Is(err, auth.ErrInvalidCredentials):
				return http.StatusUnauthorized
			case errors.Is(err, auth.ErrInvalidTOTPCode):
				return http.StatusPreconditionRequired
			case errors.Is(err, auth.ErrLoginLocked), errors.Is(err, auth.ErrTOTPLocked):
				return http.StatusTooManyRequests
			case errors.Is(err, auth.ErrInvalidEnrollmentCode), errors.Is(err, auth.ErrInvalidPassword):
				return http.StatusUnprocessableEntity
			case errors.Is(err, auth.ErrSetupComplete):
				return http.StatusConflict
			case errors.Is(err, auth.ErrForbidden):
				return http.StatusForbidden
			}

			return 0
		}),
		response.WithErrorDataFunc(ErrorPageData(sm)),
	)

	h := NewHandler(testLogger(), testReader(t), store, hw, sm, a, opts...)

	return sm.LoadAndSave(h)
}

// sessionStoreHandler is newWebHandlerWithAuth with sessions in a migrated
// database, as the server keeps them. So a test can read back what a request
// left in the session store.
func sessionStoreHandler(t *testing.T, a *auth.Auth) (http.Handler, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "sessions.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	sm := auth.NewSession(testLogger(), auth.WithSessionStore(conn))

	return newWebHandlerWithSession(t, testStore(t), a, sm), conn
}

// storedSessions returns the encoded data of every session in conn.
func storedSessions(t *testing.T, conn *sql.DB) [][]byte {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), `SELECT data FROM sessions`)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var all [][]byte

	for rows.Next() {
		var data []byte
		require.NoError(t, rows.Scan(&data))

		all = append(all, data)
	}

	require.NoError(t, rows.Err())
	require.NotEmpty(t, all, "the request should have saved a session")

	return all
}

// empty returns a handler over an inventory with no sweep.
func empty(t *testing.T) http.Handler {
	t.Helper()

	return newWebHandler(t, testStore(t))
}

// seeded returns a handler over an inventory with two swept devices.
func seeded(t *testing.T) http.Handler {
	t.Helper()

	store := testStore(t)

	swept := []scanner.Host{
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	}

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), swept)
	require.NoError(t, err)

	return newWebHandler(t, store)
}

// seededWith returns a handler over an inventory with n swept devices, for a
// test that needs more than the two of seeded.
func seededWith(t *testing.T, n int) http.Handler {
	t.Helper()

	store := testStore(t)

	swept := make([]scanner.Host, 0, n)
	for i := 1; i <= n; i++ {
		swept = append(swept, host(
			fmt.Sprintf("192.0.2.%d", i),
			fmt.Sprintf("00:00:5e:00:53:%02x", i),
			fmt.Sprintf("host-%d.example", i),
		))
	}

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), swept)
	require.NoError(t, err)

	return newWebHandler(t, store)
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))

	return rec
}

func TestOverviewRendersTheInventory(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))

	body := rec.Body.String()

	assert.Contains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, "jocasta")

	// The ledger is the only graphic of the page. Its proportion must be a
	// number. In an attribute, html/template replaces a value that it cannot
	// trust with ZgotmplZ, and gives no warning.
	assert.Contains(t, body, `class="ledger"`)
	assert.Contains(t, body, `width="100"`)
	assert.NotContains(t, body, "ZgotmplZ")

	// The page counts both swept devices. The activity log shows what the
	// sweep recorded.
	assert.Contains(t, body, "Seen recently")
	assert.Contains(t, body, "printer.local")
	assert.Contains(t, body, "discovered")

	// "Seen recently" is not a limit with no explanation.
	assert.Contains(t, body, "answered a sweep in the last 15 minutes")

	// The page names the sweep that made all this.
	assert.Contains(t, body, "test-sweep")
	assert.Contains(t, body, prefix)

	// The last-sweep panel measures freshness from the last successful
	// collection. A healthy collector gives no age warning.
	assert.Contains(t, body, "Last complete sweep")
	assert.NotContains(t, body, "presence counts can be out of date")
}

// A collector can record scan rows while each of them fails. Then the presence
// counts get old, with no visible cause. The overview tells this, and points
// at the sweep log.
func TestOverviewFlagsAStalledCollector(t *testing.T) {
	t.Parallel()

	store, conn := testStoreWithConn(t)

	swept := []scanner.Host{host("192.0.2.10", macA, "printer.local")}
	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), swept)
	require.NoError(t, err)

	// A later discovery run that finished but failed. It is newer than the
	// clean run. So the status of the panel reads from it.
	_, err = conn.ExecContext(t.Context(),
		`INSERT INTO scans (source_id, kind, status, error, started_at, finished_at)
		 VALUES ((SELECT id FROM sources WHERE name = 'test-sweep'), 'DISCOVERY', 'FAILED', 'boom',
		         '2999-01-01T00:00:00.000Z', '2999-01-01T00:00:05.000Z')`)
	require.NoError(t, err)

	body := get(t, newWebHandler(t, store), "/").Body.String()

	assert.Contains(t, body, "presence counts can be out of date")
	assert.Contains(t, body, `href="/scans"`)
}

// With only a relative time, the "Last sweep" line of the rail looks healthy.
// So a failed run must say that it failed.
func TestSweepNoteMarksAFailure(t *testing.T) {
	t.Parallel()

	now := time.Now()

	ok := sweepNote(now, &inventory.Scan{Status: dbtype.StatusOK, StartedAt: now.Add(-4 * time.Minute)})
	assert.Equal(t, "4m ago", ok)

	failed := sweepNote(now, &inventory.Scan{Status: dbtype.StatusFailed, StartedAt: now.Add(-4 * time.Minute)})
	assert.Equal(t, "4m ago · failed", failed)
}

// Every page in the signed-in shell starts with a skip link that jumps past the
// rail to the main region. It has <h2> sections below its <h1>.
func TestShellHasSkipLinkAndSectionHeadings(t *testing.T) {
	t.Parallel()

	body := get(t, seeded(t), "/").Body.String()

	skip := strings.Index(body, `<a class="skip-link" href="#main">Skip to content</a>`)
	require.Positive(t, skip)
	assert.Less(t, skip, strings.Index(body, `<aside class="sidebar"`),
		"the skip link comes before the rail, so it is the first tab stop")
	assert.Contains(t, body, `<main class="main" id="main" tabindex="-1">`)
	assert.Contains(t, body, `<h2 class="section">Networks</h2>`)
}

// No packet tells the VLAN of an address. So the operator sees the tag only on
// a segment that the router named.
func TestOverviewShowsWhatASegmentIsCalled(t *testing.T) {
	t.Parallel()

	store := testStore(t)

	require.NoError(t, store.RecordNetworks(t.Context(), []plugin.Network{{
		Prefix: netip.MustParsePrefix(prefix),
		Name:   "Home",
		VLAN:   10,
	}}))

	// The overview shows the segments only when it has devices for them.
	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, "printer.local")})
	require.NoError(t, err)

	body := get(t, newWebHandler(t, store), "/").Body.String()

	assert.Contains(t, body, prefix)
	assert.Contains(t, body, "VLAN 10")
	assert.Contains(t, body, "Home")
}

func TestOverviewShowsPortsAndServices(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), []scanner.Host{
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	})
	require.NoError(t, err)

	_, err = store.RecordPorts(t.Context(), "test-sweep", []scanner.PortScan{
		{Addr: netip.MustParseAddr("192.0.2.10"), Open: []uint16{80}, Scanned: []uint16{22, 80}},
		{Addr: netip.MustParseAddr("192.0.2.11"), Open: []uint16{22, 80}, Scanned: []uint16{22, 80}},
	})
	require.NoError(t, err)

	body := get(t, newWebHandler(t, store), "/").Body.String()

	assert.Contains(t, body, "Ports and services")
	assert.Contains(t, body, "Devices with services")
	assert.Contains(t, body, "Common services")
	assert.Contains(t, body, "http")
	assert.Contains(t, body, "ssh")
	assert.Contains(t, body, "Port scan")
	assert.Contains(t, body, "Port changes")
	assert.Contains(t, body, "started listening on")
	assert.NotContains(t, body, "Devices exposed")
}

// A prefix with no name is still a prefix. An empty chip next to it would look
// like a tag that the segment does not have.
func TestOverviewLeavesAnUnnamedSegmentBare(t *testing.T) {
	t.Parallel()

	body := get(t, seeded(t), "/").Body.String()

	assert.Contains(t, body, prefix)
	assert.NotContains(t, body, "VLAN")
}

// An empty inventory needs an explanation. The page tells the operator how the
// inventory fills, and gives a command to fill it now.
func TestOverviewWithoutAnySweepInvitesOne(t *testing.T) {
	t.Parallel()

	rec := get(t, empty(t), "/")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "No devices yet")
	assert.Contains(t, body, "jocasta scan 192.0.2.0/24 --save")

	// The invitation replaces the live block. A ledger of nothing tells less
	// than the instruction.
	assert.NotContains(t, body, `id="live"`)
	assert.NotContains(t, body, "Seen recently")

	// The topbar does not claim a refresh that the page does not run.
	assert.NotContains(t, body, "Refreshing every 30s")
}

// The poll swaps the contents of #live. So #live and the attributes of the poll
// are on the page. The fragment is only the body.
func TestOverviewLivePolls(t *testing.T) {
	t.Parallel()

	page := get(t, seeded(t), "/").Body.String()
	assert.Contains(t, page, `id="live"`)
	assert.Contains(t, page, `hx-get="/overview/live"`)
	assert.Contains(t, page, `hx-trigger="every 30s"`)
	assert.Contains(t, page, `hx-swap="innerHTML"`)

	// The topbar says that the inventory polls, and that the page
	// refreshes.
	assert.Contains(t, page, "Page refreshes every 30s")
}

// The fragment endpoint comes back alone, outside a document. It has no second
// #live inside the #live of the page.
func TestOverviewLiveServesTheBodyAlone(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/overview/live")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))

	body := rec.Body.String()

	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.NotContains(t, body, "<body>")
	assert.NotContains(t, body, `id="live"`)
	assert.Contains(t, body, "Seen recently")
	assert.Contains(t, body, "Recent")
}

// The root pattern ends in {$}. So an unknown path gets not found.
func TestUnknownPathIsNotFound(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/nope")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "This page does not exist")
}

func TestStaticFiles(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	tests := []struct {
		target      string
		contentType string
	}{
		{"/static/style.css", "text/css"},
		{"/static/js/htmx.min.js", "javascript"},
		{"/static/favicon.svg", "image/svg+xml"},
		{"/static/logo.svg", "image/svg+xml"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			t.Parallel()

			rec := get(t, h, tc.target)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Header().Get("Content-Type"), tc.contentType)
			assert.NotEmpty(t, rec.Body.Bytes())
		})
	}
}

func TestMissingStaticFile(t *testing.T) {
	t.Parallel()

	assert.Equal(t, http.StatusNotFound, get(t, seeded(t), "/static/nope.css").Code)
}

// The handlers ask for templates by name. A rename that misses one would show
// only at runtime. This test parses the set as NewHandler does. It checks that
// every name that a handler renders exists.
func TestEveryNamedTemplateExists(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(
		template.New("").
			Funcs(funcs(time.Now, 15*time.Minute)).
			ParseFS(templatesFS,
				"templates/pages/*.html.tmpl",
				"templates/partials/*.html.tmpl"),
	)

	for _, name := range []string{
		templatePageDashboard, templatePageDevices, templatePageDevice,
		templatePageNetwork, templatePageEvents, templatePageScans,
		templatePageSecurity, templatePartialLiveOverview, templatePartialDeviceRows,
		templatePartialDeviceRow, templatePartialDeviceRowForm,
		templatePartialDevicePanel, TemplateNotFound, TemplateTOTP,
		"partial/live", "partial/activity", "partial/device-filters",
		"layout/head", "layout/foot",
	} {
		assert.NotNil(t, tmpl.Lookup(name), "template %q should be parsed", name)
	}
}

func TestNavMarksTheCurrentSection(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	overview := get(t, h, "/").Body.String()
	assert.Equal(t, 1, strings.Count(overview, `aria-current="page"`), "exactly one entry should be current")
	assert.Contains(t, overview, `href="/" aria-current="page"`, "the current entry is Overview")

	// The section of the 404 page names no entry of the nav. So it marks
	// nothing as current.
	notFound := get(t, h, "/nope").Body.String()
	assert.NotContains(t, notFound, `aria-current="page"`)
}

// host builds a swept host as a sweep does. A malformed argument means a broken
// test.
func host(ip, mac, hostname string) scanner.Host {
	h, err := hosts.BuildHost(context.Background(), hosts.HostInput{IP: ip, MAC: mac, Hostname: hostname})
	if err != nil {
		panic(err)
	}

	return scanner.Host{Host: h}
}
