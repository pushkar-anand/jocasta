//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/api"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/notify"
	"github.com/pushkar-anand/jocasta/internal/server"
)

// role is an account a page is viewed as.
type role struct {
	name     string
	username string
	role     dbtype.UserRole
}

var (
	admin  = role{name: "admin", username: "e2e-admin", role: dbtype.RoleAdmin}
	writer = role{name: "writer", username: "e2e-writer", role: dbtype.RoleReadWrite}
	reader = role{name: "reader", username: "e2e-reader", role: dbtype.RoleRead}
	roles  = []role{admin, writer, reader}
)

const testPassword = "e2e-password-placeholder"

// app is the whole server over one fixture, as a deployment serves it.
type app struct {
	fixture fixture
	srv     *httptest.Server
	store   *inventory.Store
}

var (
	seedMu    sync.Mutex
	seedClock time.Time
)

// setClock moves the clock the fixture is written through; each read after
// moves it on a little, so a scan takes time.
func setClock(t time.Time) {
	seedMu.Lock()
	defer seedMu.Unlock()

	seedClock = t
}

func tick() time.Time {
	seedMu.Lock()
	defer seedMu.Unlock()

	seedClock = seedClock.Add(40 * time.Millisecond)

	return seedClock
}

// startApp builds a database in dir, fills it with f, and serves it at the
// anchor. The returned stop closes the server and the database.
func startApp(ctx context.Context, dir string, f fixture) (*app, func(), error) {
	log := slog.New(slog.DiscardHandler)
	if os.Getenv("E2E_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}

	conn, err := db.New(&db.Config{Path: dir, Name: "jocasta.db"})
	if err != nil {
		return nil, nil, err
	}

	cfg := &server.Config{Logger: log, Addr: "127.0.0.1", HomeCountry: "GB"}

	if f.seed != nil {
		writes := inventory.New(conn, log, inventory.WithClock(tick))

		rec, err := f.seed(ctx, writes, conn, log)
		if err != nil {
			_ = conn.Close()

			return nil, nil, fmt.Errorf("seed %s: %w", f.name, err)
		}

		cfg.RecentTraffic = recentAt{rec}
	}

	store := inventory.New(conn, log, inventory.WithClock(func() time.Time { return anchor }))

	a, err := auth.New(conn, models.New(conn), password.NewHasher())
	if err != nil {
		return nil, nil, err
	}

	if !f.noUsers {
		for _, r := range roles {
			if _, err := a.CreateUser(ctx, r.username, testPassword, r.role); err != nil {
				return nil, nil, err
			}
		}
	}

	if f.seed != nil {
		if cfg.Notifier, err = notifier(conn, store, log, f); err != nil {
			return nil, nil, err
		}

		if err := seedAccounts(ctx, a, f); err != nil {
			return nil, nil, err
		}
	}

	v, err := validator.New(validator.WithCustomTags(map[string]validator.ValidationFunc{
		"deviceclass": api.DeviceClassRule,
	}))
	if err != nil {
		return nil, nil, err
	}

	srv := httptest.NewServer(server.Handler(cfg, conn, store, v, a))

	return &app{fixture: f, srv: srv, store: store}, func() {
		srv.Close()
		_ = conn.Close()
	}, nil
}

// recentAt answers the map's "what is active" from the recorder as of the
// anchor, which the map asks relative to its own clock.
type recentAt struct{ rec *inventory.TrafficRecorder }

func (r recentAt) Recent(since time.Time) []inventory.RecentEdge {
	return r.rec.Recent(since)
}

func notifier(conn *sql.DB, store *inventory.Store, log *slog.Logger, f fixture) (*notify.Notifier, error) {
	off := false

	cfgs := map[string]notify.Config{
		"phone": {Ntfy: &notify.Ntfy{URL: "https://ntfy.example.com/jocasta", Priority: 3}},
		"hooks": {Webhook: &notify.Webhook{URL: "https://hooks.example.com/jocasta", Secret: "placeholder-secret"}},
	}

	if f.name == fixtureWeird.name {
		cfgs["a-destination-with-a-name-long-enough-to-wrap-twice"] = notify.Config{HTTP: &notify.HTTP{
			URL:  "https://chat.example.com/api/webhooks/" + strings.Repeat("0123456789", 8),
			Body: `{"content": {{ json .Body }}}`,
		}}
		cfgs["switched-off"] = notify.Config{Enabled: &off, Ntfy: &notify.Ntfy{URL: "https://ntfy.example.com/off"}}
	}

	var dests []*notify.Destination

	for _, name := range []string{"phone", "hooks", "a-destination-with-a-name-long-enough-to-wrap-twice", "switched-off"} {
		c, ok := cfgs[name]
		if !ok || !c.On() {
			continue
		}

		d, err := notify.NewDestination(name, c)
		if err != nil {
			return nil, err
		}

		dests = append(dests, d)
	}

	return notify.New(conn, store, log, dests...), nil
}

// seedAccounts gives the admin some tokens, and the weird fixture more
// accounts and tokens than either list is laid out for.
func seedAccounts(ctx context.Context, a *auth.Auth, f fixture) error {
	users, err := a.ListUsers(ctx)
	if err != nil {
		return err
	}

	var adminID int64

	for _, u := range users {
		if u.Username == admin.username {
			adminID = u.ID
		}
	}

	tokens := []string{"home-assistant", "backup script"}
	if f.name == fixtureWeird.name {
		tokens = append(tokens, strings.Repeat("a-token-name-with-no-spaces-", 3))
		for i := range 20 {
			tokens = append(tokens, fmt.Sprintf("token %02d for the nightly export job", i))
		}

		for i := range 15 {
			name := fmt.Sprintf("user-%02d", i)
			if i == 0 {
				name = strings.Repeat("long-username-", 8)[:100]
			}

			if _, err := a.CreateUser(ctx, name, testPassword, []dbtype.UserRole{dbtype.RoleRead, dbtype.RoleReadWrite}[i%2]); err != nil {
				return err
			}
		}
	}

	for i, name := range tokens {
		scope := dbtype.TokenRead
		if i%2 == 1 {
			scope = dbtype.TokenReadWrite
		}

		if _, _, err := a.CreateToken(ctx, adminID, name, scope); err != nil {
			return err
		}
	}

	return nil
}

// signIn returns the session cookies for r, signed in through the login form.
func (a *app) signIn(ctx context.Context, r role) ([]*http.Cookie, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	form := url.Values{"username": {r.username}, "password": {testPassword}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.srv.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	_ = res.Body.Close()

	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("sign in as %s: %s", r.username, res.Status)
	}

	u, _ := url.Parse(a.srv.URL)

	return jar.Cookies(u), nil
}
