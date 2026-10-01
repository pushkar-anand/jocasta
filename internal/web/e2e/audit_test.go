//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestAudit draws every page in every state on every screen, theme and
// fixture, and writes each screenshot with a report of what the checks found.
// It fails nothing: it is for reading.
//
//	E2E_AUDIT=$PWD/tmp/audit TMPDIR=$PWD/tmp go test -tags e2e -run TestAudit -timeout 0 ./internal/web/e2e/
//
// TMPDIR moves the databases and Chrome's profiles off /tmp, which a full run
// can fill.
//
// E2E_FIXTURES, E2E_ROLES, E2E_VIEWPORTS, E2E_THEMES and E2E_PAGES narrow the
// run to comma-separated names.
func TestAudit(t *testing.T) {
	out := os.Getenv("E2E_AUDIT")
	if out == "" {
		t.Skip("E2E_AUDIT names the directory to write to")
	}

	if err := os.MkdirAll(out, 0o750); err != nil { //nolint:gosec // the directory this run was pointed at
		t.Fatal(err)
	}

	fixtures := pick("E2E_FIXTURES", []fixture{fixtureEmpty, fixtureNormal, fixtureWeird, fixtureFresh}, func(f fixture) string { return f.name })
	rs := pick("E2E_ROLES", []role{admin}, func(r role) string { return r.name }, roles...)
	vs := pick("E2E_VIEWPORTS", viewports, func(v viewport) string { return v.name })
	ts := pick("E2E_THEMES", themes, func(s string) string { return s })

	type job struct {
		a       *app
		r       role
		cookies []*http.Cookie
		v       viewport
		theme   string
		p       page
		s       state
	}

	var jobs []job

	for _, f := range fixtures {
		a, stop, err := startApp(t.Context(), t.TempDir(), f)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()

		ps, err := pages(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}

		ps = pick("E2E_PAGES", ps, func(p page) string { return p.name })

		for _, r := range rs {
			var cookies []*http.Cookie
			if !f.noUsers {
				if cookies, err = a.signIn(t.Context(), r); err != nil {
					t.Fatal(err)
				}
			}

			for _, p := range ps {
				if p.signedOut && r.name != rs[0].name {
					continue
				}

				c := cookies
				if p.signedOut {
					c = nil
				}

				for _, v := range vs {
					for _, theme := range ts {
						for _, s := range p.states {
							jobs = append(jobs, job{a, r, c, v, theme, p, s})
						}
					}
				}
			}
		}
	}

	workers := 6
	if n, err := strconv.Atoi(os.Getenv("E2E_PARALLEL")); err == nil && n > 0 {
		workers = n
	}

	var (
		mu    sync.Mutex
		shots []*shot
		wg    sync.WaitGroup
		queue = make(chan job)
	)

	// Each worker has a Chrome of its own, so one that falls over takes only
	// its own page with it; the worker starts another and tries once more.
	for range workers {
		wg.Go(func() {
			var b *browser

			defer func() {
				if b != nil {
					b.cancel()
				}
			}()

			// Draining the queue even when Chrome will not start keeps the
			// sender below from blocking on a worker that gave up.
			for j := range queue {
				sh, err := retry(t, &b, func(b *browser) (*shot, error) {
					return capture(b, j.a, j.r, j.cookies, j.v, j.theme, j.p, j.s)
				})
				if errors.Is(err, errNotApplicable) {
					continue
				}

				if err != nil {
					t.Errorf("%s/%s/%s/%s/%s: %v", j.a.fixture.name, j.r.name, j.p.name, j.s.name, j.v.name, err)

					continue
				}

				if err := os.WriteFile(filepath.Join(out, sh.file()), sh.png, 0o600); err != nil { //nolint:gosec // under the directory this run was pointed at
					t.Error(err)
				}

				sh.png = nil

				mu.Lock()

				shots = append(shots, sh)
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

	sort.Slice(shots, func(i, j int) bool { return shots[i].file() < shots[j].file() })

	if err := writeReport(filepath.Join(out, "report.json"), shots); err != nil {
		t.Fatal(err)
	}

	t.Log(summary(shots))
}

// retry runs capture on *b, starting a Chrome when there is none, and once
// more on a fresh one when the first attempt fails: a Chrome that fell over
// fails every page after it.
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

// pick narrows all to the names listed in env, or keeps def when env is
// unset. extra are further choices env may name that def leaves out.
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

// summary counts each check's findings, and on how many pages.
func summary(shots []*shot) string {
	type tally struct {
		n     int
		pages map[string]bool
	}

	by := map[string]*tally{}

	for _, s := range shots {
		for _, p := range s.Problems {
			t := by[p.Check]
			if t == nil {
				t = &tally{pages: map[string]bool{}}
				by[p.Check] = t
			}

			t.n++
			t.pages[s.Page] = true
		}
	}

	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder

	fmt.Fprintf(&b, "%d screenshots\n", len(shots))

	for _, k := range keys {
		fmt.Fprintf(&b, "%-32s %5d on %d pages\n", k, by[k].n, len(by[k].pages))
	}

	return b.String()
}
