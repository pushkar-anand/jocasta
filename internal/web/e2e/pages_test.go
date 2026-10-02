//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/chromedp/chromedp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// page is one screen, at a path worked out from the fixture it is shown over.
type page struct {
	name string
	path string

	// signedOut pages are drawn without a session.
	signedOut bool

	// writes pages need an account that can change things; a reader is
	// turned away.
	admin bool

	states []state
}

// state is something done to a loaded page before it is checked: a dialog
// opened, a menu dropped down, a row put into edit.
type state struct {
	name string
	do   chromedp.Action

	// status is the response this state is meant to end on, when it is an
	// error page by design.
	status int
}

var base = state{name: "base"}

// errNotApplicable is a state that has nothing to act on in this fixture,
// such as editing a row of an empty list.
var errNotApplicable = errors.New("state does not apply")

func click(sel string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var found bool

		err := chromedp.Evaluate(fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			if (!el) return false;
			el.click();
			return true;
		})()`, sel), &found).Do(ctx)
		if err != nil {
			return err
		}

		if !found {
			return errNotApplicable
		}

		return nil
	})
}

var (
	openDetails = state{name: "details-open", do: chromedp.Evaluate(
		`document.querySelectorAll('details:not(.usermenu)').forEach((d) => { d.open = true; })`, nil)}
	openUserMenu = state{name: "user-menu", do: click("details.usermenu > summary")}
	editRow      = state{name: "row-edit", do: click(`[hx-get$="/edit"]`)}

	// openDrawer applies only where the menu button shows: below 60rem.
	openDrawer = state{name: "drawer", do: chromedp.ActionFunc(func(ctx context.Context) error {
		var shown bool

		err := chromedp.Evaluate(`(() => {
			const el = document.querySelector('.topbar__menu');
			return !!el && getComputedStyle(el).display !== 'none';
		})()`, &shown).Do(ctx)
		if err != nil {
			return err
		}

		if !shown {
			return errNotApplicable
		}

		return click(".topbar__menu").Do(ctx)
	})}
)

func dialog(id string) state {
	return state{name: "dialog-" + id, do: click(`[data-open="` + id + `"]`)}
}

// pages lists every screen to visit over a. Device and network pages are
// picked by hardware address and prefix, so the list names the same devices
// whatever ids the fixture's writes gave them.
func pages(ctx context.Context, a *app) ([]page, error) {
	ps := []page{
		{name: "login", path: "/login", signedOut: true, states: []state{base, {name: "failed", do: chromedp.Tasks{
			chromedp.SetValue(`input[name=username]`, "nobody", chromedp.ByQuery),
			chromedp.SetValue(`input[name=password]`, "wrong-password", chromedp.ByQuery),
			click(`form button[type=submit]`),
		}, status: 401}}},
		{name: "overview", path: "/", states: []state{base, openUserMenu, openDrawer}},
		{name: "devices", path: "/devices", states: []state{base, editRow}},
		{name: "devices-page2", path: "/devices?page=2", states: []state{base}},
		{name: "devices-nomatch", path: "/devices?q=no-such-device-anywhere", states: []state{base}},
		{name: "devices-quiet", path: "/devices?status=offline&sort=name", states: []state{base}},
		{name: "traffic", path: "/traffic", states: []state{base, openDetails}},
		{name: "traffic-internet", path: "/traffic?tab=internet", states: []state{base, openDetails}},
		{name: "traffic-broadcasts", path: "/traffic?tab=broadcasts", states: []state{base}},
		{name: "traffic-30d", path: "/traffic?window=30d", states: []state{base}},
		{name: "map", path: "/map", states: []state{base}},
		{name: "map-world", path: "/map?view=world", states: []state{base}},
		{name: "topology", path: "/topology", states: []state{base}},
		{name: "events", path: "/events", states: []state{base}},
		{name: "scans", path: "/scans", states: []state{base}},
		{name: "security", path: "/settings/security", states: []state{base}},
		{name: "tokens", path: "/settings/tokens", states: []state{base, dialog("token-dialog")}},
		{name: "users", path: "/settings/users", admin: true, states: []state{base, dialog("user-dialog")}},
		{name: "notifications", path: "/settings/notifications", admin: true, states: []state{base}},
		{name: "notfound", path: "/no-such-page", states: []state{{name: "base", status: 404}}},
	}

	if a.fixture.noUsers {
		return []page{{name: "setup", path: "/setup", signedOut: true, states: []state{base}}}, nil
	}

	devices, err := a.store.ListDevices(ctx, inventory.DeviceFilter{})
	if err != nil {
		return nil, err
	}

	byMAC := map[string]int64{}
	for _, d := range devices {
		byMAC[d.MAC] = d.ID
	}

	var wanted []struct{ name, mac string }

	switch a.fixture.name {
	case fixtureNormal.name:
		wanted = []struct{ name, mac string }{
			{"device-nas", "00:00:5e:00:53:80"},
			{"device-workstation", "00:00:5e:00:53:10"},
			{"device-tv", "00:00:5e:00:53:30"},
			{"device-unnamed", "00:00:5e:00:53:a9"},
			{"device-gateway", "00:00:5e:00:53:01"},
		}
	case fixtureWeird.name:
		wanted = []struct{ name, mac string }{
			{"device-long", "00:00:5e:00:53:02"},
			{"device-fqdn", "00:00:5e:00:53:03"},
			{"device-unbroken", "00:00:5e:00:53:04"},
			{"device-markup", "00:00:5e:00:53:05"},
			{"device-emoji", "00:00:5e:00:53:06"},
			{"device-rtl", "00:00:5e:00:53:07"},
			{"device-bare", "00:00:5e:00:53:09"},
			{"device-ancient", "00:00:5e:00:53:0a"},
			{"device-huge", "00:00:5e:00:53:0c"},
		}
	}

	for _, w := range wanted {
		id, ok := byMAC[w.mac]
		if !ok {
			return nil, fmt.Errorf("%s fixture has no device %s", a.fixture.name, w.mac)
		}

		path := "/devices/" + strconv.FormatInt(id, 10)
		ps = append(ps,
			page{name: w.name, path: path, states: []state{base, openDetails}},
			page{name: w.name + "-internet", path: path + "?tab=internet", states: []state{base}},
		)
	}

	networks, err := a.store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	for i, n := range networks {
		ps = append(ps, page{name: "network-" + strconv.Itoa(i), path: "/networks/" + strconv.FormatInt(n.ID, 10), states: []state{base}})
	}

	return ps, nil
}
