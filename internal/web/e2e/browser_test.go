//go:build e2e

package e2e

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

var (
	//go:embed testdata/invariants.js
	invariantsJS string

	//go:embed testdata/axe.min.js
	axeJS string
)

// viewport is a screen on which a page is drawn. The sizes are on each side of
// the breakpoints of the stylesheet (34rem, 40rem, 60rem, 700px).
type viewport struct {
	name  string
	w, h  int64
	dpr   float64
	touch bool
}

var (
	phoneSmall = viewport{name: "phone-360", w: 360, h: 740, dpr: 3, touch: true}
	phone      = viewport{name: "phone-390", w: 390, h: 844, dpr: 3, touch: true}
	tablet     = viewport{name: "tablet-768", w: 768, h: 1024, dpr: 2, touch: true}
	small      = viewport{name: "laptop-1024", w: 1024, h: 768, dpr: 1}
	laptop     = viewport{name: "laptop-1366", w: 1366, h: 768, dpr: 1}
	retina     = viewport{name: "laptop-1440", w: 1440, h: 900, dpr: 2}
	desktop    = viewport{name: "desktop-1920", w: 1920, h: 1080, dpr: 1}

	viewports = []viewport{phoneSmall, phone, tablet, small, laptop, retina, desktop}
)

var themes = []string{"light", "dark"}

// shot is one page in one state, as one role saw it on one screen.
type shot struct {
	Fixture  string    `json:"fixture"`
	Role     string    `json:"role"`
	Viewport string    `json:"viewport"`
	Theme    string    `json:"theme"`
	Page     string    `json:"page"`
	State    string    `json:"state"`
	URL      string    `json:"url"`
	Problems []problem `json:"problems"`

	png []byte

	// golden is the first screen at 1x, for comparison with the stored
	// picture. It is nil when this shot has none.
	golden []byte
}

// problem is one fault that a check found.
type problem struct {
	Check  string `json:"check"`
	El     string `json:"el"`
	Detail string `json:"detail"`
}

// browser is one Chrome. Either this code started it, or it is at
// E2E_CHROME_URL.
type browser struct {
	ctx    context.Context
	cancel func()
}

func newBrowser(ctx context.Context) (*browser, error) {
	var (
		alloc  context.Context
		cancel context.CancelFunc
	)

	if u := os.Getenv("E2E_CHROME_URL"); u != "" {
		alloc, cancel = remote.NewAllocator(ctx, u)
	} else {
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("hide-scrollbars", true),
			chromedp.Flag("font-render-hinting", "none"),
			chromedp.Flag("force-color-profile", "srgb"),
		)
		if p := os.Getenv("E2E_CHROME"); p != "" {
			opts = append(opts, chromedp.ExecPath(p))
		}

		alloc, cancel = chromedp.NewExecAllocator(ctx, opts...)
	}

	bctx, bcancel := chromedp.NewContext(alloc, chromedp.WithErrorf(quietErrorf))
	if err := chromedp.Do(bctx); err != nil {
		bcancel()
		cancel()

		return nil, fmt.Errorf("start chrome: %w", err)
	}

	return &browser{ctx: bctx, cancel: func() { bcancel(); cancel() }}, nil
}

// quietErrorf logs the errors of chromedp, except the events that it has no
// handler for yet. Newer Chrome sends these for every open dialog.
func quietErrorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if strings.Contains(msg, "unhandled node event") {
		return
	}

	log.Print(msg)
}

// tab is the listening for one page load: everything that the console and the
// network said while it ran.
type tab struct {
	ctx    context.Context
	mu     sync.Mutex
	errors []problem
}

func (b *browser) newTab(cookies []*http.Cookie, origin string) (*tab, func(), error) {
	ctx, cancel := chromedp.NewContext(b.ctx)
	t := &tab{ctx: ctx}

	listen(ctx, t, runtime.ExceptionThrown, func(e runtime.EventExceptionThrown) *problem {
		// The text that cdproto gave the exception before it dropped Error.
		// The known findings match on it.
		d := e.ExceptionDetails

		detail := fmt.Sprintf("exception %q (%d:%d)", d.Text, d.LineNumber, d.ColumnNumber)
		if d.Exception != nil {
			detail += ": " + d.Exception.Description
		}

		return &problem{Check: "script-error", Detail: detail}
	})
	listen(ctx, t, runtime.ConsoleAPICalled, func(e runtime.EventConsoleAPICalled) *problem {
		if e.Type != runtime.ConsoleAPICalledTypeError {
			return nil
		}

		var parts []string
		for _, a := range e.Args {
			parts = append(parts, strings.Trim(string(a.Value), `"`))
		}

		return &problem{Check: "console-error", Detail: strings.Join(parts, " ")}
	})
	listen(ctx, t, cdplog.EntryAdded, func(e cdplog.EventEntryAdded) *problem {
		if e.Entry.Level != cdplog.EntryLevelError {
			return nil
		}

		return &problem{Check: "console-error", Detail: e.Entry.Text + " " + e.Entry.URL}
	})
	listen(ctx, t, network.ResponseReceived, func(e network.EventResponseReceived) *problem {
		if e.Response.Status < 400 {
			return nil
		}

		return &problem{Check: "http-error", Detail: fmt.Sprintf("%d %s", e.Response.Status, e.Response.URL)}
	})
	listen(ctx, t, network.LoadingFailed, func(e network.EventLoadingFailed) *problem {
		if e.Canceled {
			return nil
		}

		return &problem{Check: "request-failed", Detail: e.ErrorText}
	})

	err := chromedp.Do(ctx,
		cmd(network.Enable, network.EnableParams{}),
		cmd(cdplog.Enable, cdp.Empty{}),
		// Tabs in one Chrome share its cookies. So a signed-out page drawn
		// after a signed-in page would still find the session.
		cmd(network.ClearBrowserCookies, cdp.Empty{}),
		chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
			for _, c := range cookies {
				_, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{
					Name: c.Name, Value: c.Value, URL: origin, HTTPOnly: &c.HttpOnly,
				})
				if err != nil {
					return err
				}
			}

			return nil
		}),
	)
	if err != nil {
		cancel()

		return nil, nil, err
	}

	return t, cancel, nil
}

// listen adds a problem to the tab for each event that report turns into one.
func listen[E any](ctx context.Context, t *tab, ev cdp.Event[E], report func(E) *problem) {
	events := chromedp.Events(ctx, ev)

	go func() {
		for e, err := range events {
			if err != nil {
				return
			}

			if p := report(e); p != nil {
				t.mu.Lock()
				t.errors = append(t.errors, *p)
				t.mu.Unlock()
			}
		}
	}()
}

// cmd makes an action that sends one protocol command.
func cmd[P, R any](c cdp.Command[P, R], params P) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, _ *chromedp.Target) error {
		_, err := chromedp.Call(ctx, c, params)

		return err
	})
}

func (t *tab) drain() []problem {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := t.errors
	t.errors = nil

	return out
}

// emulate sets the screen and the colour scheme, with reduced motion. So a
// screenshot never catches a transition in the middle.
func emulate(v viewport, theme string) chromedp.Action[chromedp.Void] {
	opts := []chromedp.EmulateViewportOption{chromedp.EmulateScale(v.dpr)}
	if v.touch {
		opts = append(opts, chromedp.EmulateMobile, chromedp.EmulateTouch)
	}

	return chromedp.Steps(
		chromedp.EmulateViewport(v.w, v.h, opts...),
		cmd(emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{Features: []*emulation.MediaFeature{
			{Name: "prefers-color-scheme", Value: theme},
			{Name: "prefers-reduced-motion", Value: "reduce"},
		}}),
	)
}

// settle waits for the fonts, and for htmx to finish what the page load
// started.
func settle() chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		deadline := time.Now().Add(10 * time.Second)

		for time.Now().Before(deadline) {
			done, err := chromedp.Evaluate[bool](`document.readyState === 'complete' && document.fonts.status === 'loaded' && !document.querySelector('.htmx-request, .htmx-settling, .htmx-swapping')`)(ctx, t)
			if err != nil {
				return err
			}

			if done {
				_, err := chromedp.Sleep(150*time.Millisecond)(ctx, t)

				return err
			}

			if _, err := chromedp.Sleep(50*time.Millisecond)(ctx, t); err != nil {
				return err
			}
		}

		return fmt.Errorf("page did not settle")
	})
}

// check runs the layout checks and the WCAG AA rules of axe against the
// current page.
func check(v viewport, out *[]problem) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		expr := fmt.Sprintf("(%s)({touch: %t})", strings.TrimSpace(invariantsJS), v.touch)

		layout, err := chromedp.Evaluate[[]problem](expr)(ctx, t)
		if err != nil {
			return fmt.Errorf("invariants: %w", err)
		}

		*out = append(*out, layout...)

		if _, err := chromedp.Evaluate[chromedp.Void](axeJS)(ctx, t); err != nil {
			return fmt.Errorf("load axe: %w", err)
		}

		axe, err := chromedp.Evaluate[[]problem](`(async () => {
			const modal = [...document.querySelectorAll('dialog[open]')].find((d) => d.matches(':modal'));
			const r = await axe.run(modal || document, {
				runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa']},
				resultTypes: ['violations'],
			});
			const out = [];
			for (const v of r.violations) for (const n of v.nodes) {
				out.push({check: 'axe:' + v.id, el: n.target.join(' '), detail: (n.failureSummary || v.help).replace(/\s+/g, ' ').slice(0, 300)});
			}
			return out;
		})()`, chromedp.EvalAwaitPromise)(ctx, t)
		if err != nil {
			return fmt.Errorf("axe: %w", err)
		}

		*out = append(*out, axe...)

		return nil
	})
}

// capture loads one page in one state, checks it, and takes a screenshot.
func capture(b *browser, a *app, r role, cookies []*http.Cookie, v viewport, theme string, p page, s state, golden bool) (*shot, error) {
	t, cancel, err := b.newTab(cookies, a.srv.URL)
	if err != nil {
		return nil, err
	}
	defer cancel()

	ctx, timeout := context.WithTimeout(t.ctx, 60*time.Second)
	defer timeout()

	sh := &shot{
		Fixture: a.fixture.name, Role: r.name, Viewport: v.name, Theme: theme,
		Page: p.name, State: s.name, URL: p.path, Problems: []problem{},
	}

	tasks := []chromedp.Action[chromedp.Void]{
		// A background tab gets no frames. A screenshot waits for one.
		cmd(cdppage.BringToFront, cdp.Empty{}),
		emulate(v, theme),
		chromedp.Navigate(a.srv.URL + p.path),
		settle(),
	}

	if s.do != nil {
		tasks = append(tasks, s.do, settle())
	}

	tasks = append(tasks,
		check(v, &sh.Problems),
		screenshot(v, &sh.png),
	)

	if golden {
		tasks = append(tasks, firstScreen(v, &sh.golden))
	}

	if os.Getenv("E2E_TIMING") != "" {
		timed := make([]chromedp.Action[chromedp.Void], 0, len(tasks))
		for i, a := range tasks {
			timed = append(timed, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
				start := time.Now()
				_, err := a(ctx, t)

				fmt.Printf("%s/%s step %d: %s\n", p.name, s.name, i, time.Since(start))

				return err
			}))
		}

		tasks = timed
	}

	if err := chromedp.Do(ctx, tasks...); err != nil {
		return nil, fmt.Errorf("%s %s: %w", p.name, s.name, err)
	}

	// An error page answers with its status on purpose. The console also
	// reports it. This is a state that ends on an error page, and an admin
	// page drawn for an account that is not admin.
	status := s.status
	if p.admin && r.role != dbtype.RoleAdmin {
		status = http.StatusForbidden
	}

	for _, pr := range t.drain() {
		if status != 0 && strings.Contains(pr.Detail, strconv.Itoa(status)) {
			continue
		}

		sh.Problems = append(sh.Problems, pr)
	}

	return sh, nil
}

// maxShotPixels limits the height of a screenshot in device pixels. A full page
// of the device list of the weird fixture is more than 150,000 at 3x. This
// crashes the compositor of Chrome.
const maxShotPixels = 16000

// screenshot captures the page from the top, as far down as maxShotPixels
// allows.
func screenshot(v viewport, out *[]byte) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		h, err := chromedp.Evaluate[float64](`document.documentElement.scrollHeight`)(ctx, t)
		if err != nil {
			return err
		}

		if h*v.dpr <= maxShotPixels {
			*out, err = chromedp.FullScreenshot(100)(ctx, t)

			return err
		}

		// Too tall at this density. Draw it again at 1x. This keeps the whole
		// page on most pages.
		if _, err := chromedp.EmulateViewport(v.w, v.h, chromedp.EmulateScale(1))(ctx, t); err != nil {
			return err
		}

		yes := true

		r, err := chromedp.Call(ctx, cdppage.CaptureScreenshot, cdppage.CaptureScreenshotParams{
			Format:                cdppage.CaptureScreenshotFormatPng,
			CaptureBeyondViewport: &yes,
			FromSurface:           &yes,
			Clip:                  &cdppage.Viewport{Width: float64(v.w), Height: min(h, maxShotPixels), Scale: 1},
		})
		*out = r.Data

		return err
	})
}

// firstScreen captures what a reader sees before a scroll, at 1x. So a stored
// picture stays small. It hides the text caret, because the caret blinks.
func firstScreen(v viewport, out *[]byte) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		opts := []chromedp.EmulateViewportOption{chromedp.EmulateScale(1)}
		if v.touch {
			opts = append(opts, chromedp.EmulateMobile, chromedp.EmulateTouch)
		}

		_, err := chromedp.Steps(
			chromedp.EmulateViewport(v.w, v.h, opts...),
			// Through the CSSOM. The content security policy refuses a
			// <style> element, and a test must not need to remove it.
			chromedp.Evaluate[chromedp.Void](`(() => {
				if (document.activeElement) document.activeElement.style.caretColor = 'transparent';
				window.scrollTo(0, 0);
			})()`),
			chromedp.Sleep(100*time.Millisecond),
		)(ctx, t)
		if err != nil {
			return err
		}

		yes := true

		r, err := chromedp.Call(ctx, cdppage.CaptureScreenshot, cdppage.CaptureScreenshotParams{
			Format: cdppage.CaptureScreenshotFormatPng, FromSurface: &yes,
		})
		*out = r.Data

		return err
	})
}

func (s *shot) file() string {
	return strings.Join([]string{s.Fixture, s.Role, s.Page, s.State, s.Viewport, s.Theme}, "_") + ".png"
}

func writeReport(path string, shots []*shot) error {
	b, err := json.MarshalIndent(shots, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, b, 0o600) //nolint:gosec // under the directory this run was pointed at
}
