//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// TestSearchKeepsFocus types into each live search box. It checks that the box
// still has focus and every character when the results come back. It checks
// that an empty box searches again. It also checks that a select keeps focus
// when it fetches.
func TestSearchKeepsFocus(t *testing.T) {
	ctx := t.Context()

	a, stop, err := startApp(ctx, t.TempDir(), fixtureNormal)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	b, err := newBrowser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.cancel()

	cookies, err := a.signIn(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ name, path, input, sel string }{
		{"devices", "/devices", `#device-filters input[name=q]`, `#device-filters select[name=group]`},
		{"device traffic", "/devices/4", `#device-traffic input[name=q]`, `#traffic-dir`},
	} {
		t.Run(c.name, func(t *testing.T) {
			tb, cancel, err := b.newTab(cookies, a.srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()

			tctx, timeout := context.WithTimeout(tb.ctx, 30*time.Second)
			defer timeout()

			var (
				focused bool
				value   string
				reqs    int
			)

			err = chromedp.Do(tctx,
				cmd(cdppage.BringToFront, cdp.Empty{}),
				emulate(laptop, "light"),
				chromedp.Navigate(a.srv.URL+c.path),
				settle(),
				chromedp.Evaluate[chromedp.Void](`window.__reqs = 0; document.addEventListener('htmx:beforeRequest', () => window.__reqs++)`),
				chromedp.Click(chromedp.CSS(c.input)),
				// Type with pauses longer than the debounce. So a request is
				// active when the next key arrives.
				chromedp.SendKeys(chromedp.CSS(c.input), "l"),
				chromedp.Sleep(400*time.Millisecond),
				chromedp.SendKeys(chromedp.CSS(c.input), "a"),
				chromedp.Sleep(400*time.Millisecond),
				chromedp.SendKeys(chromedp.CSS(c.input), "p"),
				chromedp.Sleep(time.Second),
				settle(),
				into(&focused, chromedp.Evaluate[bool](`document.activeElement === document.querySelector(`+"`"+c.input+"`"+`)`)),
				into(&value, chromedp.Evaluate[string](`document.querySelector(`+"`"+c.input+"`"+`).value`)),
				into(&reqs, chromedp.Evaluate[int](`window.__reqs`)),
			)
			if err != nil {
				t.Fatal(err)
			}

			if os.Getenv("E2E_LOG") != "" {
				t.Logf("focused=%v value=%q requests=%d", focused, value, reqs)
			}

			if !focused {
				t.Error("search box lost focus")
			}

			if value != "lap" {
				t.Errorf("search box holds %q, want %q", value, "lap")
			}

			// Fewer than three characters is too broad for a search.
			if reqs != 1 {
				t.Errorf("%d requests for one three-letter word, want 1", reqs)
			}

			var selFocused bool

			err = chromedp.Do(tctx,
				chromedp.SendKeys(chromedp.CSS(c.input), kb.Backspace+kb.Backspace+kb.Backspace),
				chromedp.Sleep(time.Second),
				settle(),
				into(&reqs, chromedp.Evaluate[int](`window.__reqs`)),
				chromedp.Evaluate[chromedp.Void](`(() => {
					const el = document.querySelector(`+"`"+c.sel+"`"+`);
					el.focus();
					el.value = el.options[1].value;
					el.dispatchEvent(new Event('change', {bubbles: true}));
				})()`),
				chromedp.Sleep(500*time.Millisecond),
				settle(),
				into(&selFocused, chromedp.Evaluate[bool](`document.activeElement === document.querySelector(`+"`"+c.sel+"`"+`)`)),
			)
			if err != nil {
				t.Fatal(err)
			}

			if reqs != 2 {
				t.Errorf("%d requests after emptying the box, want 2: one for the word, one to clear it", reqs)
			}

			if !selFocused {
				t.Error("select lost focus after it fetched")
			}
		})
	}
}

// into makes an action that stores the value of a in v. So a value can come
// out of a list of steps.
func into[T any](v *T, a chromedp.Action[T]) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		var err error

		*v, err = a(ctx, t)

		return err
	})
}
