//go:build e2e

package e2e

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// TestFixturesRender asks for every page over every fixture and expects each
// to render, so a fixture that trips a template fails here before any browser
// starts.
func TestFixturesRender(t *testing.T) {
	for _, f := range []fixture{fixtureEmpty, fixtureNormal, fixtureWeird, fixtureFresh} {
		t.Run(f.name, func(t *testing.T) {
			a, stop, err := startApp(t.Context(), t.TempDir(), f)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()

			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}

			c := &http.Client{Jar: jar}

			if f.noUsers {
				if status := get(t, c, a.srv.URL+"/setup"); status != http.StatusOK {
					t.Errorf("/setup: %d", status)
				}

				return
			}

			cookies, err := a.signIn(t.Context(), admin)
			if err != nil {
				t.Fatal(err)
			}

			u, _ := url.Parse(a.srv.URL)
			jar.SetCookies(u, cookies)

			paths := []string{
				"/", "/devices", "/devices?page=2", "/traffic", "/traffic?tab=internet", "/traffic?window=30d",
				"/map", "/map?view=world", "/topology", "/events", "/scans",
				"/settings/security", "/settings/tokens", "/settings/users", "/settings/notifications",
			}

			devices, err := a.store.ListDevices(t.Context(), inventory.DeviceFilter{})
			if err != nil {
				t.Fatal(err)
			}

			for _, d := range devices {
				id := strconv.FormatInt(d.ID, 10)
				paths = append(paths, "/devices/"+id, "/devices/"+id+"?tab=internet")
			}

			networks, err := a.store.ListNetworks(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			for _, n := range networks {
				paths = append(paths, "/networks/"+strconv.FormatInt(n.ID, 10))
			}

			for _, p := range paths {
				if status := get(t, c, a.srv.URL+p); status != http.StatusOK {
					t.Errorf("%s: %d", p, status)
				}
			}
		})
	}
}

func get(t *testing.T, c *http.Client, u string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = res.Body.Close()

	return res.StatusCode
}
