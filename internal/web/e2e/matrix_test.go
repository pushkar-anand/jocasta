//go:build e2e

package e2e

import (
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var allFixtures = []fixture{fixtureEmpty, fixtureNormal, fixtureWeird, fixtureFresh}

// matrix is every combination of fixture, account, screen and theme in which a
// run draws each page.
type matrix struct {
	fixtures  []fixture
	roles     []role
	viewports []viewport
	themes    []string

	// golden picks the jobs whose first screen is compared with a stored
	// picture. Nil picks none.
	golden func(job) bool
}

type job struct {
	a       *app
	r       role
	cookies []*http.Cookie
	v       viewport
	theme   string
	p       page
	s       state
}

// jobs starts an app over each fixture, and lists every page state to draw.
// The apps stop when t ends.
func (m matrix) jobs(t *testing.T) []job {
	t.Helper()

	var jobs []job

	for _, f := range m.fixtures {
		a, stop, err := startApp(t.Context(), t.TempDir(), f)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(stop)

		ps, err := pages(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}

		ps = pick("E2E_PAGES", ps, func(p page) string { return p.name })

		for _, r := range m.roles {
			var cookies []*http.Cookie
			if !f.noUsers {
				if cookies, err = a.signIn(t.Context(), r); err != nil {
					t.Fatal(err)
				}
			}

			for _, p := range ps {
				// A signed-out page looks the same to every account.
				if p.signedOut && r.name != m.roles[0].name {
					continue
				}

				c := cookies
				if p.signedOut {
					c = nil
				}

				for _, v := range m.viewports {
					for _, theme := range m.themes {
						for _, s := range p.states {
							jobs = append(jobs, job{a, r, c, v, theme, p, s})
						}
					}
				}
			}
		}
	}

	return jobs
}

// run draws every job on E2E_PARALLEL workers (6 by default). It gives each
// shot to done, one at a time. It skips a state with nothing to act on in its
// fixture.
func (m matrix) run(t *testing.T, done func(*shot)) {
	t.Helper()

	jobs := m.jobs(t)

	workers := 6
	if n, err := strconv.Atoi(os.Getenv("E2E_PARALLEL")); err == nil && n > 0 {
		workers = n
	}

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		queue = make(chan job)
	)

	// Each worker has its own Chrome. So a Chrome that crashes loses only its
	// own page. The worker starts a new Chrome and tries once more.
	for range workers {
		wg.Go(func() {
			var b *browser

			defer func() {
				if b != nil {
					b.cancel()
				}
			}()

			// Empty the queue, also when Chrome does not start. So the sender
			// below does not block on a worker that stopped.
			for j := range queue {
				golden := m.golden != nil && m.golden(j)

				sh, err := retry(t, &b, func(b *browser) (*shot, error) {
					return capture(b, j.a, j.r, j.cookies, j.v, j.theme, j.p, j.s, golden)
				})
				if errors.Is(err, errNotApplicable) {
					continue
				}

				if err != nil {
					t.Errorf("%s/%s/%s/%s/%s: %v", j.a.fixture.name, j.r.name, j.p.name, j.s.name, j.v.name, err)

					continue
				}

				mu.Lock()
				done(sh)
				mu.Unlock()
			}
		})
	}

	for i, j := range jobs {
		if i%100 == 0 {
			t.Logf("%d/%d", i, len(jobs))
		}

		queue <- j
	}

	close(queue)
	wg.Wait()
}

// retry runs capture on *b. It starts a Chrome when there is none. When the
// first attempt fails, it tries once more on a new Chrome. A Chrome that
// crashed fails every page after it.
func retry(t *testing.T, b **browser, capture func(*browser) (*shot, error)) (*shot, error) {
	t.Helper()

	var lastErr error

	for range 2 {
		if *b == nil {
			nb, err := newBrowser(t.Context())
			if err != nil {
				lastErr = err

				continue
			}

			*b = nb
		}

		sh, err := capture(*b)
		if err == nil || errors.Is(err, errNotApplicable) {
			return sh, err
		}

		lastErr = err

		(*b).cancel()
		*b = nil
	}

	return nil, lastErr
}

// pick limits all to the names in env. When env is unset, it keeps def. extra
// are more choices that env can name, and that def leaves out.
func pick[T any](env string, def []T, name func(T) string, extra ...T) []T {
	want := os.Getenv(env)
	if want == "" {
		return def
	}

	names := strings.Split(want, ",")

	var out []T

	for _, c := range append(slices.Clone(def), extra...) {
		if slices.Contains(names, name(c)) && !slices.ContainsFunc(out, func(o T) bool { return name(o) == name(c) }) {
			out = append(out, c)
		}
	}

	return out
}
