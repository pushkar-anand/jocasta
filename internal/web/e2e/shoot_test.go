//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// shotSpec is one picture of one element, for an issue write-up.
type shotSpec struct {
	Name     string `json:"name"`
	Fixture  string `json:"fixture"`
	Role     string `json:"role"`
	Path     string `json:"path"`
	Viewport string `json:"viewport"`
	Theme    string `json:"theme"`

	// Do is script run once the page settles, such as a click that opens a
	// dialog.
	Do string `json:"do"`

	// Selector picks what to frame; empty frames the viewport.
	Selector string `json:"selector"`
	Pad      int    `json:"pad"`
}

// TestShoot takes the pictures E2E_SHOOT lists (a JSON array of shotSpec)
// and writes them to E2E_SHOOT_OUT.
//
//	E2E_SHOOT=specs.json E2E_SHOOT_OUT=plans/issues/img go test -tags e2e -run TestShoot ./internal/web/e2e/
func TestShoot(t *testing.T) {
	in, out := os.Getenv("E2E_SHOOT"), os.Getenv("E2E_SHOOT_OUT")
	if in == "" || out == "" {
		t.Skip("E2E_SHOOT and E2E_SHOOT_OUT name the specs and where to write")
	}

	raw, err := os.ReadFile(in) //nolint:gosec // the spec file this run was pointed at
	if err != nil {
		t.Fatal(err)
	}

	var specs []shotSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(out, 0o750); err != nil { //nolint:gosec // the directory this run was pointed at
		t.Fatal(err)
	}

	apps := map[string]*app{}

	for _, f := range []fixture{fixtureEmpty, fixtureNormal, fixtureWeird, fixtureFresh} {
		for _, s := range specs {
			if s.Fixture != f.name || apps[f.name] != nil {
				continue
			}

			a, stop, err := startApp(t.Context(), t.TempDir(), f)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()

			apps[f.name] = a
		}
	}

	b, err := newBrowser(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer b.cancel()

	for _, s := range specs {
		if err := shoot(b, apps[s.Fixture], s, filepath.Join(out, s.Name+".png")); err != nil {
			t.Errorf("%s: %v", s.Name, err)
		}
	}
}

func shoot(b *browser, a *app, s shotSpec, file string) error {
	var v viewport

	for _, c := range viewports {
		if c.name == s.Viewport {
			v = c
		}
	}

	if v.name == "" {
		return fmt.Errorf("no viewport %q", s.Viewport)
	}

	var r role

	for _, c := range roles {
		if c.name == s.Role {
			r = c
		}
	}

	cookies, err := a.signIn(b.ctx, r)
	if r.name == "" || a.fixture.noUsers {
		cookies, err = nil, nil
	}

	if err != nil {
		return err
	}

	t, cancel, err := b.newTab(cookies, a.srv.URL)
	if err != nil {
		return err
	}
	defer cancel()

	ctx, timeout := context.WithTimeout(t.ctx, 60*time.Second)
	defer timeout()

	theme := s.Theme
	if theme == "" {
		theme = "light"
	}

	var png []byte

	err = chromedp.Run(ctx,
		cdppage.BringToFront(),
		emulate(v, theme),
		chromedp.Navigate(a.srv.URL+s.Path),
		settle(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if s.Do == "" {
				return nil
			}

			if err := chromedp.Evaluate(s.Do, nil).Do(ctx); err != nil {
				return err
			}

			return settle().Do(ctx)
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			clip := &cdppage.Viewport{Width: float64(v.w), Height: float64(v.h), Scale: 1}

			if s.Selector != "" {
				var box struct{ X, Y, W, H float64 }

				err := chromedp.Evaluate(fmt.Sprintf(`(() => {
					const el = document.querySelector(%q);
					if (!el) return null;
					const r = el.getBoundingClientRect();
					return {X: r.left + scrollX, Y: r.top + scrollY, W: r.width, H: r.height};
				})()`, s.Selector), &box).Do(ctx)
				if err != nil {
					return err
				}

				if box.W == 0 {
					return fmt.Errorf("nothing matches %s", s.Selector)
				}

				pad := float64(s.Pad)
				clip.X = max(box.X-pad, 0)
				clip.Y = max(box.Y-pad, 0)
				clip.Width = min(box.W+2*pad, 4000)
				clip.Height = min(box.H+2*pad, maxShotPixels/v.dpr)
			}

			var err error

			png, err = cdppage.CaptureScreenshot().
				WithFormat(cdppage.CaptureScreenshotFormatPng).
				WithCaptureBeyondViewport(true).
				WithFromSurface(true).
				WithClip(clip).
				Do(ctx)

			return err
		}),
	)
	if err != nil {
		return err
	}

	return os.WriteFile(file, png, 0o600) //nolint:gosec // under the directory this run was pointed at
}
