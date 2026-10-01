//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

	m := matrix{
		fixtures:  pick("E2E_FIXTURES", allFixtures, func(f fixture) string { return f.name }),
		roles:     pick("E2E_ROLES", []role{admin}, func(r role) string { return r.name }, roles...),
		viewports: pick("E2E_VIEWPORTS", viewports, func(v viewport) string { return v.name }),
		themes:    pick("E2E_THEMES", themes, func(s string) string { return s }),
	}

	var shots []*shot

	m.run(t, func(sh *shot) {
		if err := os.WriteFile(filepath.Join(out, sh.file()), sh.png, 0o600); err != nil { //nolint:gosec // under the directory this run was pointed at
			t.Error(err)
		}

		sh.png = nil
		shots = append(shots, sh)
	})

	sort.Slice(shots, func(i, j int) bool { return shots[i].file() < shots[j].file() })

	if err := writeReport(filepath.Join(out, "report.json"), shots); err != nil {
		t.Fatal(err)
	}

	t.Log(summary(shots))
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
