//go:build e2e

package e2e

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
)

//go:embed testdata/known.json
var knownJSON []byte

// known is a fault the checks find today and an open issue covers. A fix
// deletes its entries, so the gate holds the line from then on.
type known struct {
	// Issue names the write-up that covers it, such as "C3".
	Issue string `json:"issue"`
	Check string `json:"check"`

	// El, when set, must appear in the element's description once
	// normalised (see norm).
	El string `json:"el,omitempty"`

	// Detail, when set, must appear in the finding's detail.
	Detail string `json:"detail,omitempty"`

	// Page and Viewport are globs over "fixture/page/state" and the
	// viewport name; empty matches any.
	Page     string `json:"page,omitempty"`
	Viewport string `json:"viewport,omitempty"`
}

var (
	quoted = regexp.MustCompile(`"[^"]*"`)
	digits = regexp.MustCompile(`\d+`)
)

// norm drops what varies between runs and fixtures from an element's
// description: its text and any numbers, such as a row id.
func norm(el string) string {
	return strings.TrimSpace(digits.ReplaceAllString(quoted.ReplaceAllString(el, ""), "N"))
}

func (k known) covers(s *shot, p problem) bool {
	if k.Check != p.Check {
		return false
	}

	if k.El != "" && !strings.Contains(norm(p.El), k.El) {
		return false
	}

	if k.Detail != "" && !strings.Contains(p.Detail, k.Detail) {
		return false
	}

	if k.Page != "" {
		if ok, _ := path.Match(k.Page, s.Fixture+"/"+s.Page+"/"+s.State); !ok {
			return false
		}
	}

	if k.Viewport != "" {
		if ok, _ := path.Match(k.Viewport, s.Viewport); !ok {
			return false
		}
	}

	return true
}

// TestE2E draws every page in every state and fails on any finding the known
// list does not cover, and on a first screen that no longer matches its
// stored picture (see isGolden). By default it draws what a pull request
// needs: every fixture as admin on a phone, a tablet and a laptop, in both
// themes. E2E_FULL=1 draws every screen and every account, and also fails on
// a known entry that matched nothing, so a fix cannot leave its entry behind.
// E2E_UPDATE=1 stores the pictures taken instead of comparing them, and
// E2E_ARTIFACTS names a directory for the pictures that differ.
func TestE2E(t *testing.T) {
	var list []known
	if err := json.Unmarshal(knownJSON, &list); err != nil {
		t.Fatal(err)
	}

	full := os.Getenv("E2E_FULL") != ""

	m := matrix{fixtures: allFixtures, roles: []role{admin}, viewports: []viewport{phone, tablet, laptop}, themes: themes, golden: isGolden}
	if full {
		m.roles, m.viewports = roles, viewports
	}

	used := make([]bool, len(list))

	update := os.Getenv("E2E_UPDATE") != ""
	artifacts := os.Getenv("E2E_ARTIFACTS")

	if update {
		if err := os.MkdirAll(goldenDir, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	if artifacts != "" {
		if err := os.MkdirAll(artifacts, 0o750); err != nil { //nolint:gosec // the directory this run was pointed at
			t.Fatal(err)
		}
	}

	m.run(t, func(sh *shot) {
		if sh.golden != nil {
			if err := compareGolden(sh, update, artifacts); err != nil {
				t.Error(err)
			}
		}

		var unknown []string

		for _, p := range sh.Problems {
			covered := false

			for i, k := range list {
				if k.covers(sh, p) {
					used[i], covered = true, true

					break
				}
			}

			if !covered {
				unknown = append(unknown, fmt.Sprintf("  %s %s: %s", p.Check, p.El, p.Detail))
			}
		}

		if len(unknown) > 0 {
			t.Errorf("%s:\n%s", strings.TrimSuffix(sh.file(), ".png"), strings.Join(unknown, "\n"))
		}
	})

	if !full {
		return
	}

	for i, k := range list {
		if !used[i] {
			t.Errorf("known entry for %s matched nothing; delete it: %+v", k.Issue, k)
		}
	}
}
