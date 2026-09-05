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

// Addresses come from RFC 5737 and hardware addresses from RFC 7042, both
// reserved for documentation, so nothing here names a real device.
const (
	prefix = "192.0.2.0/24"

	macA = "00:00:5e:00:53:01"
	macB = "00:00:5e:00:53:02"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testStore opens an inventory over a migrated database scoped to the test.
func testStore(t *testing.T) *inventory.Store {
	t.Helper()

	store, _ := testStoreWithConn(t)

	return store
}

// testStoreWithConn also hands back the connection, for a test that has to set
// up a state the store's own writes cannot reach.
func testStoreWithConn(t *testing.T) (*inventory.Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return inventory.New(conn, testLogger()), conn
}

// testReader builds the request reader the server wires in.
func testReader(t *testing.T) *request.Reader {
	t.Helper()

	// deviceclass is the one custom tag any form or query struct in this
	// package uses; main.go registers the same rule for the real server.
	v, err := validator.New(validator.WithCustomTags(map[string]validator.ValidationFunc{
		"deviceclass": api.DeviceClassRule,
	}))
	require.NoError(t, err)

	return request.NewReader(testLogger(), v)
}

// testUsername and testPassword name the one account seeded into every test
// Auth, so a test that needs a signed-in view doesn't have to invent its own
// credential.
const (
	testUsername = "jocasta-test"
	testPassword = "test-password-1"
)

// testAuth builds an Auth over its own migrated database, separate from
// whatever store a test is exercising, seeded with the one account a test
// signs in as.
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

	a, err := auth.New(q, password.NewHasher())
	require.NoError(t, err)

	return a
}

// unseededAuth is testAuth without seeding an account -- for a test exercising
// the setup flow itself, which only makes sense before any account exists.
func unseededAuth(t *testing.T) *auth.Auth {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "auth.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	a, err := auth.New(models.New(conn), password.NewHasher())
	require.NoError(t, err)

	return a
}

// newWebHandler builds the web handler the way the server does: an HTML writer
// with the error pages configured, and the templates attached inside
// NewHandler, wrapped in the same session load-and-save the real server
// applies outside it -- without it, any code touching the session panics on
// finding no session data in the request's context.
func newWebHandler(t *testing.T, store *inventory.Store) http.Handler {
	t.Helper()

	return newWebHandlerWithAuth(t, store, testAuth(t))
}

// newWebHandlerWithAuth is newWebHandler for a test that needs its own handle
// on the Auth it signs in against -- to look up what a token's create just
// gave the user's id, say, rather than scraping it back out of a response.
func newWebHandlerWithAuth(t *testing.T, store *inventory.Store, a *auth.Auth) http.Handler {
	t.Helper()

	hw := response.NewHTMLWriter(testLogger(), nil,
		response.WithErrorTemplates(map[int]string{
			http.StatusNotFound:     TemplateNotFound,
			http.StatusUnauthorized: TemplateLogin,
			http.StatusConflict:     TemplateSetup,
			http.StatusForbidden:    TemplateForbidden,
		}),
		response.WithErrorStatusMapper(func(err error) int {
			switch {
			case errors.Is(err, inventory.ErrNotFound):
				return http.StatusNotFound
			case errors.Is(err, auth.ErrInvalidCredentials):
				return http.StatusUnauthorized
			case errors.Is(err, auth.ErrSetupComplete):
				return http.StatusConflict
			case errors.Is(err, auth.ErrForbidden):
				return http.StatusForbidden
			}

			return http.StatusInternalServerError
		}),
		response.WithErrorDataFunc(ErrorPageData),
	)

	sm := auth.NewSession()
	h := NewHandler(testLogger(), testReader(t), store, hw, sm, a)

	return sm.LoadAndSave(h)
}

// empty returns a handler over an inventory nothing has swept into.
func empty(t *testing.T) http.Handler {
	t.Helper()

	return newWebHandler(t, testStore(t))
}

// seeded returns a handler over an inventory holding two swept devices.
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

// seededWith returns a handler over an inventory holding n swept devices, for a
// test that needs more of them than the pair seeded gives.
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

	// The ledger is the page's one graphic, and its proportion has to come out
	// as a number: html/template replaces a value it cannot vouch for in an
	// attribute with ZgotmplZ rather than failing.
	assert.Contains(t, body, `class="ledger"`)
	assert.Contains(t, body, `width="100"`)
	assert.NotContains(t, body, "ZgotmplZ")

	// Both swept devices are counted, and the activity log shows what the
	// sweep recorded.
	assert.Contains(t, body, "Seen recently")
	assert.Contains(t, body, "printer.local")
	assert.Contains(t, body, "discovered")

	// The sweep that produced all this is named.
	assert.Contains(t, body, "test-sweep")
	assert.Contains(t, body, prefix)
}

// Nothing on the wire says which VLAN an address is on, so a segment the
// router named is the only place the operator ever sees the tag.
func TestOverviewShowsWhatASegmentIsCalled(t *testing.T) {
	t.Parallel()

	store := testStore(t)

	require.NoError(t, store.RecordNetworks(t.Context(), []plugin.Network{{
		Prefix: netip.MustParsePrefix(prefix),
		Name:   "Home",
		VLAN:   10,
	}}))

	// The overview shows the segments only once it has devices to put on them.
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

	assert.Contains(t, body, "Ports &amp; services")
	assert.Contains(t, body, "Devices with services")
	assert.Contains(t, body, "Common services")
	assert.Contains(t, body, "http")
	assert.Contains(t, body, "ssh")
	assert.Contains(t, body, "Port scan")
	assert.Contains(t, body, "Recent changes")
	assert.Contains(t, body, "began answering on")
	assert.NotContains(t, body, "Devices exposed")
}

// A prefix nobody has named is still a prefix, and showing an empty chip
// beside it would read as a tag the segment does not have.
func TestOverviewLeavesAnUnnamedSegmentBare(t *testing.T) {
	t.Parallel()

	body := get(t, seeded(t), "/").Body.String()

	assert.Contains(t, body, prefix)
	assert.NotContains(t, body, "VLAN")
}

// An empty inventory is a state to explain, not a blank page: the operator is
// told how the inventory gets filled and given a command to fill it now.
func TestOverviewWithoutAnySweepInvitesOne(t *testing.T) {
	t.Parallel()

	rec := get(t, empty(t), "/")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "No devices yet")
	assert.Contains(t, body, "jocasta scan 192.168.1.0/24 --save")

	// The invitation replaces the live block rather than sitting under an empty
	// one: a ledger of nothing says less than the instruction does.
	assert.NotContains(t, body, `id="live"`)
	assert.NotContains(t, body, "Seen recently")
}

// The poll swaps the contents of #live, so #live and the attributes that drive
// the poll live on the page and the fragment is the body alone.
func TestOverviewLivePolls(t *testing.T) {
	t.Parallel()

	page := get(t, seeded(t), "/").Body.String()
	assert.Contains(t, page, `id="live"`)
	assert.Contains(t, page, `hx-get="/overview/live"`)
	assert.Contains(t, page, `hx-trigger="every 30s"`)
	assert.Contains(t, page, `hx-swap="innerHTML"`)
}

// The fragment endpoint comes back on its own rather than wrapped in a document,
// and without a second #live nested inside the one the page keeps.
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

// The root pattern ends in {$}, so an unknown path is reported rather than
// quietly served the overview.
func TestUnknownPathIsNotFound(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/nope")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "There is nothing at this address")
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

// The handlers ask for templates by name; a rename that misses one would only
// show up at runtime. This parses the set the way NewHandler does and checks
// every name a handler renders is defined.
func TestEveryNamedTemplateExists(t *testing.T) {
	t.Parallel()

	tmpl := template.Must(
		template.New("").
			Funcs(funcs(time.Now)).
			ParseFS(templatesFS,
				"templates/pages/*.html.tmpl",
				"templates/partials/*.html.tmpl"),
	)

	for _, name := range []string{
		templatePageDashboard, templatePageDevices, templatePageDevice,
		templatePageNetwork, templatePageEvents, templatePageScans,
		templatePartialLiveOverview, templatePartialDeviceRows,
		templatePartialDeviceRow, templatePartialDeviceRowForm,
		templatePartialDevicePanel, TemplateNotFound,
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

	// The 404 page's section names none of the nav's entries, so it marks
	// nothing current rather than falling back to the first one.
	notFound := get(t, h, "/nope").Body.String()
	assert.NotContains(t, notFound, `aria-current="page"`)
}

// host builds a swept host the way a sweep does. A malformed argument is a
// broken test.
func host(ip, mac, hostname string) scanner.Host {
	h, err := hosts.BuildHost(context.Background(), hosts.HostInput{IP: ip, MAC: mac, Hostname: hostname})
	if err != nil {
		panic(err)
	}

	return scanner.Host{Host: h}
}
