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

	"github.com/chromedp/cdproto/cdp"
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

	// Do is a script that runs when the page settles, such as a click that
	// opens a dialog.
	Do string `json:"do"`

	// Selector picks what to frame. Empty frames the viewport.
	Selector string `json:"selector"`
	Pad      int    `json:"pad"`
}

// TestShoot takes the pictures that E2E_SHOOT lists (a JSON array of shotSpec).
// It writes them to E2E_SHOOT_OUT.
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

	err = chromedp.Do(ctx,
		cmd(cdppage.BringToFront, cdp.Empty{}),
		emulate(v, theme),
		chromedp.Navigate(a.srv.URL+s.Path),
		settle(),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			if s.Do == "" {
				return nil
			}

			if _, err := chromedp.Evaluate[chromedp.Void](s.Do)(ctx, t); err != nil {
				return err
			}

			_, err := settle()(ctx, t)

			return err
		}),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			clip := &cdppage.Viewport{Width: float64(v.w), Height: float64(v.h), Scale: 1}

			if s.Selector != "" {
				box, err := chromedp.Evaluate[struct{ X, Y, W, H float64 }](fmt.Sprintf(`(() => {
					const el = document.querySelector(%q);
					if (!el) return null;
					const r = el.getBoundingClientRect();
					return {X: r.left + scrollX, Y: r.top + scrollY, W: r.width, H: r.height};
				})()`, s.Selector))(ctx, t)
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

			yes := true

			r, err := chromedp.Call(ctx, cdppage.CaptureScreenshot, cdppage.CaptureScreenshotParams{
				Format:                cdppage.CaptureScreenshotFormatPng,
				CaptureBeyondViewport: &yes,
				FromSurface:           &yes,
				Clip:                  clip,
			})
			png = r.Data

			return err
		}),
	)
	if err != nil {
		return err
	}

	return os.WriteFile(file, png, 0o600) //nolint:gosec // under the directory this run was pointed at
}
