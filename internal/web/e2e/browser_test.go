//go:build e2e

package e2e

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

var (
	//go:embed testdata/invariants.js
	invariantsJS string

	//go:embed testdata/axe.min.js
	axeJS string
)

// viewport is a screen a page is drawn on. The sizes sit either side of the
// stylesheet's breakpoints (34rem, 40rem, 60rem, 700px).
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
}

// problem is one fault a check found.
type problem struct {
	Check  string `json:"check"`
	El     string `json:"el"`
	Detail string `json:"detail"`
}

// browser is one Chrome, either started here or reached at E2E_CHROME_URL.
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
		alloc, cancel = chromedp.NewRemoteAllocator(ctx, u)
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

	bctx, bcancel := chromedp.NewContext(alloc)
	if err := chromedp.Run(bctx); err != nil {
		bcancel()
		cancel()

		return nil, fmt.Errorf("start chrome: %w", err)
	}

	return &browser{ctx: bctx, cancel: func() { bcancel(); cancel() }}, nil
}

// tab is one page load's worth of listening: everything the console and the
// network said while it ran.
type tab struct {
	ctx    context.Context
	mu     sync.Mutex
	errors []problem
}

func (b *browser) newTab(cookies []*http.Cookie, origin string) (*tab, func(), error) {
	ctx, cancel := chromedp.NewContext(b.ctx)
	t := &tab{ctx: ctx}

	chromedp.ListenTarget(ctx, func(ev any) {
		t.mu.Lock()
		defer t.mu.Unlock()

		switch e := ev.(type) {
		case *runtime.EventExceptionThrown:
			t.errors = append(t.errors, problem{Check: "script-error", Detail: e.ExceptionDetails.Error()})
		case *runtime.EventConsoleAPICalled:
			if e.Type == runtime.APITypeError {
				var parts []string
				for _, a := range e.Args {
					parts = append(parts, strings.Trim(string(a.Value), `"`))
				}

				t.errors = append(t.errors, problem{Check: "console-error", Detail: strings.Join(parts, " ")})
			}
		case *cdplog.EventEntryAdded:
			if e.Entry.Level == cdplog.LevelError {
				t.errors = append(t.errors, problem{Check: "console-error", Detail: e.Entry.Text + " " + e.Entry.URL})
			}
		case *network.EventResponseReceived:
			if e.Response.Status >= 400 {
				t.errors = append(t.errors, problem{Check: "http-error", Detail: fmt.Sprintf("%d %s", e.Response.Status, e.Response.URL)})
			}
		case *network.EventLoadingFailed:
			if !e.Canceled {
				t.errors = append(t.errors, problem{Check: "request-failed", Detail: e.ErrorText})
			}
		}
	})

	err := chromedp.Run(ctx,
		network.Enable(),
		cdplog.Enable(),
		// Tabs in one Chrome share its cookies, so a signed-out page drawn
		// after a signed-in one would find the session still there.
		network.ClearBrowserCookies(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			for _, c := range cookies {
				err := network.SetCookie(c.Name, c.Value).WithURL(origin).WithHTTPOnly(c.HttpOnly).Do(ctx)
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

func (t *tab) drain() []problem {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := t.errors
	t.errors = nil

	return out
}

// emulate sets the screen and the colour scheme, with motion reduced so a
// screenshot never catches a transition halfway.
func emulate(v viewport, theme string) chromedp.Action {
	opts := []chromedp.EmulateViewportOption{chromedp.EmulateScale(v.dpr)}
	if v.touch {
		opts = append(opts, chromedp.EmulateMobile, chromedp.EmulateTouch)
	}

	return chromedp.Tasks{
		chromedp.EmulateViewport(v.w, v.h, opts...),
		emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
			{Name: "prefers-color-scheme", Value: theme},
			{Name: "prefers-reduced-motion", Value: "reduce"},
		}),
	}
}

// settle waits for the fonts and for htmx to finish what the page load
// started.
func settle() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		deadline := time.Now().Add(10 * time.Second)

		for time.Now().Before(deadline) {
			var done bool

			err := chromedp.Evaluate(`document.readyState === 'complete' && document.fonts.status === 'loaded' && !document.querySelector('.htmx-request, .htmx-settling, .htmx-swapping')`, &done).Do(ctx)
			if err != nil {
				return err
			}

			if done {
				return chromedp.Sleep(150 * time.Millisecond).Do(ctx)
			}

			if err := chromedp.Sleep(50 * time.Millisecond).Do(ctx); err != nil {
				return err
			}
		}

		return fmt.Errorf("page did not settle")
	})
}

// check runs the layout checks and axe's WCAG AA rules against the page as it
// stands.
func check(v viewport, out *[]problem) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var layout []problem

		expr := fmt.Sprintf("(%s)({touch: %t})", strings.TrimSpace(invariantsJS), v.touch)
		if err := chromedp.Evaluate(expr, &layout).Do(ctx); err != nil {
			return fmt.Errorf("invariants: %w", err)
		}

		*out = append(*out, layout...)

		if err := chromedp.Evaluate(axeJS, nil).Do(ctx); err != nil {
			return fmt.Errorf("load axe: %w", err)
		}

		var axe []problem

		err := chromedp.Evaluate(`(async () => {
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
		})()`, &axe, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }).Do(ctx)
		if err != nil {
			return fmt.Errorf("axe: %w", err)
		}

		*out = append(*out, axe...)

		return nil
	})
}

// capture loads one page in one state, checks it, and screenshots it.
func capture(b *browser, a *app, r role, cookies []*http.Cookie, v viewport, theme string, p page, s state) (*shot, error) {
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

	tasks := chromedp.Tasks{
		// A background tab gets no frames, and a screenshot waits for one.
		cdppage.BringToFront(),
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

	if os.Getenv("E2E_TIMING") != "" {
		timed := make(chromedp.Tasks, 0, len(tasks))
		for i, a := range tasks {
			timed = append(timed, chromedp.ActionFunc(func(ctx context.Context) error {
				start := time.Now()
				err := a.Do(ctx)

				fmt.Printf("%s/%s step %d: %s\n", p.name, s.name, i, time.Since(start))

				return err
			}))
		}

		tasks = timed
	}

	if err := chromedp.Run(ctx, tasks); err != nil {
		return nil, fmt.Errorf("%s %s: %w", p.name, s.name, err)
	}

	for _, pr := range t.drain() {
		// An error page by design answers with its status, which the
		// console reports too.
		if s.status != 0 && strings.Contains(pr.Detail, strconv.Itoa(s.status)) {
			continue
		}

		sh.Problems = append(sh.Problems, pr)
	}

	return sh, nil
}

// maxShotPixels caps a screenshot's height in device pixels. A full page of
// the weird fixture's device list runs past 150,000 at 3x, which crashes
// Chrome's compositor.
const maxShotPixels = 16000

// screenshot captures the page from the top, as far down as maxShotPixels
// allows.
func screenshot(v viewport, out *[]byte) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var h float64
		if err := chromedp.Evaluate(`document.documentElement.scrollHeight`, &h).Do(ctx); err != nil {
			return err
		}

		if h*v.dpr <= maxShotPixels {
			return chromedp.FullScreenshot(out, 100).Do(ctx)
		}

		// Too tall at this density: drawn again at 1x, which keeps the whole
		// page on more pages than not.
		if err := chromedp.EmulateViewport(v.w, v.h, chromedp.EmulateScale(1)).Do(ctx); err != nil {
			return err
		}

		clip := &cdppage.Viewport{Width: float64(v.w), Height: min(h, maxShotPixels), Scale: 1}

		var err error

		*out, err = cdppage.CaptureScreenshot().
			WithFormat(cdppage.CaptureScreenshotFormatPng).
			WithCaptureBeyondViewport(true).
			WithFromSurface(true).
			WithClip(clip).
			Do(ctx)

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
