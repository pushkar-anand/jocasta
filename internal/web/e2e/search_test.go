//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// TestSearchKeepsFocus types into each live search box and checks the box
// still has focus and every character once the results have come back, that
// emptying the box searches again, and that a select keeps focus when it
// fetches.
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

			err = chromedp.Run(tctx,
				cdppage.BringToFront(),
				emulate(laptop, "light"),
				chromedp.Navigate(a.srv.URL+c.path),
				settle(),
				chromedp.Evaluate(`window.__reqs = 0; document.addEventListener('htmx:beforeRequest', () => window.__reqs++)`, nil),
				chromedp.Click(c.input, chromedp.ByQuery),
				// Typed with pauses longer than the debounce, so a request is
				// in flight while the next key lands.
				chromedp.SendKeys(c.input, "l", chromedp.ByQuery),
				chromedp.Sleep(400*time.Millisecond),
				chromedp.SendKeys(c.input, "a", chromedp.ByQuery),
				chromedp.Sleep(400*time.Millisecond),
				chromedp.SendKeys(c.input, "p", chromedp.ByQuery),
				chromedp.Sleep(time.Second),
				settle(),
				chromedp.Evaluate(`document.activeElement === document.querySelector(`+"`"+c.input+"`"+`)`, &focused),
				chromedp.Evaluate(`document.querySelector(`+"`"+c.input+"`"+`).value`, &value),
				chromedp.Evaluate(`window.__reqs`, &reqs),
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

			// Fewer than three characters is too broad to search on.
			if reqs != 1 {
				t.Errorf("%d requests for one three-letter word, want 1", reqs)
			}

			var selFocused bool

			err = chromedp.Run(tctx,
				chromedp.SendKeys(c.input, kb.Backspace+kb.Backspace+kb.Backspace, chromedp.ByQuery),
				chromedp.Sleep(time.Second),
				settle(),
				chromedp.Evaluate(`window.__reqs`, &reqs),
				chromedp.Evaluate(`(() => {
					const el = document.querySelector(`+"`"+c.sel+"`"+`);
					el.focus();
					el.value = el.options[1].value;
					el.dispatchEvent(new Event('change', {bubbles: true}));
				})()`, nil),
				chromedp.Sleep(500*time.Millisecond),
				settle(),
				chromedp.Evaluate(`document.activeElement === document.querySelector(`+"`"+c.sel+"`"+`)`, &selFocused),
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
