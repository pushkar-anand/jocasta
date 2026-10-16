package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// editor is a handler over the seeded inventory, with the cookies of a
// signed-in admin. Only an account that can write can use the routes of these
// tests: the PATCH curate endpoints and the edit form. So an unauthenticated
// request gets the forbidden page.
func editor(t *testing.T) (http.Handler, []*http.Cookie) {
	t.Helper()

	h := seeded(t)

	return h, signIn(t, h)
}

// patch sends a form as htmx does, with the signed-in cookies.
func patch(t *testing.T, h http.Handler, cookies []*http.Cookie, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	for _, c := range cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestDeviceRowEditServesTheRowAsAForm(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	rec := requestAs(t, h, cookies, http.MethodGet, "/devices/1/edit", "")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, `hx-patch="/devices/1/row"`)
	assert.Contains(t, body, `id="device-row-1"`)
	assert.Contains(t, body, `name="label"`)
	assert.Contains(t, body, `name="group"`)

	// A cancel asks for the row back.
	assert.Contains(t, body, `hx-get="/devices/1/row"`)

	// The identity cell stays. So the edited row is still easy to see.
	assert.Contains(t, body, `class="editing__id"`)
	assert.Contains(t, body, "printer.local")
	assert.Contains(t, body, `class="dot `)
}

// A form that shows only two fields still applies all of them. So the hidden
// fields must go with it. If not, the edit would clear them.
func TestDeviceRowFormCarriesTheFieldsItDoesNotShow(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	saved := patch(t, h, cookies, "/devices/1", url.Values{
		"label":   {"Office printer"},
		"notes":   {"Second floor."},
		"type":    {"printer"},
		"ignored": {"1"},
	})
	require.Equal(t, http.StatusOK, saved.Code)

	form := requestAs(t, h, cookies, http.MethodGet, "/devices/1/edit", "").Body.String()

	assert.Contains(t, form, `name="notes" value="Second floor."`)
	assert.Contains(t, form, `name="type" value="printer"`)
	assert.Contains(t, form, `name="ignored" value="1"`)

	// A send without changes leaves them as they were.
	rec := patch(t, h, cookies, "/devices/1/row", url.Values{
		"label":   {"Office printer"},
		"group":   {""},
		"notes":   {"Second floor."},
		"type":    {"printer"},
		"ignored": {"1"},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	page := get(t, h, "/devices/1").Body.String()
	assert.Contains(t, page, "Second floor.")
	assert.Contains(t, page, "printer")
}

func TestUpdateDeviceRowAnswersWithTheRow(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	rec := patch(t, h, cookies, "/devices/1/row", url.Values{"label": {"Office printer"}})

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	// The response is only the row.
	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.NotContains(t, body, "hx-patch")
	assert.Contains(t, body, `id="device-row-1"`)
	assert.Contains(t, body, "Office printer")

	// The row that a swap puts back must be editable again. It must give focus
	// back to its Edit button when the inline form closes.
	assert.Contains(t, body, `hx-get="/devices/1/edit"`)
	assert.Contains(t, body, `data-focus-after-swap=".btn--icon"`)

	// It still shows what the sweep found. An edit does not change it.
	assert.Contains(t, body, "192.0.2.10")
	assert.Contains(t, body, macA)
}

func TestUpdateDeviceAnswersWithThePanel(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	rec := patch(t, h, cookies, "/devices/1", url.Values{
		"label": {"Office printer"},
		"group": {"office"},
		"notes": {"Hallway."},
	})

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, `id="device-panel"`)

	// The "Details saved." status has a tabindex. So the swap can put focus on
	// the outcome.
	assert.Contains(t, body, `<p class="saved" role="status" tabindex="-1">Details saved.</p>`)

	// After a save, the edit form closes again. The facts come first.
	assert.Contains(t, body, `<details class="editpanel">`)

	// The layout draws the heading, outside this swap. So the panel updates it
	// out of band. A new label shows where the device is named, and in the
	// field.
	assert.Contains(t, body, `<h1 id="page-title" title="Office printer" hx-swap-oob="true">Office printer</h1>`)
	assert.Contains(t, body, "Hallway.")

	// Only a swapped fragment can report the save.
	assert.Contains(t, body, "Details saved.")
}

func TestDevicePageDoesNotClaimToHaveSaved(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, get(t, seeded(t), "/devices/1").Body.String(), "Details saved.")
}

func TestCurationSurvivesInTheList(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	patch(t, h, cookies, "/devices/1/row", url.Values{"label": {"Office printer"}, "group": {"office"}})

	list := get(t, h, "/devices").Body.String()
	assert.Contains(t, list, "Office printer")
	assert.Contains(t, list, "office")

	// The group field can now suggest the group.
	assert.Contains(t, list, `<option value="office">`)

	// The list can also filter on it.
	filtered := get(t, h, "/devices?group=office").Body.String()
	assert.Contains(t, filtered, "Office printer")
	assert.NotContains(t, filtered, "nas.local")
}

func TestIgnoringADeviceHidesItFromTheList(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	patch(t, h, cookies, "/devices/1", url.Values{"ignored": {"1"}})

	assert.NotContains(t, get(t, h, "/devices").Body.String(), "printer.local")
	assert.Contains(t, get(t, h, "/devices?ignored=1").Body.String(), "printer.local")
}

func TestEditingUnknownDeviceIsNotFound(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	for _, target := range []string{"/devices/4040/edit", "/devices/4040/row", "/devices/abc/edit"} {
		t.Run(target, func(t *testing.T) {
			assert.Equal(t, http.StatusNotFound, requestAs(t, h, cookies, http.MethodGet, target, "").Code)
		})
	}

	for _, target := range []string{"/devices/4040", "/devices/4040/row", "/devices/abc/row"} {
		t.Run("patch "+target, func(t *testing.T) {
			rec := patch(t, h, cookies, target, url.Values{"label": {"Nothing"}})
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestCurationFromForm(t *testing.T) {
	t.Parallel()

	got := deviceEdit{
		Label: "Office printer", Notes: "Second floor.",
		Group: "office", Type: "printer", Ignored: true,
	}.toCuration()

	assert.Equal(t, "Office printer", got.Label)
	assert.Equal(t, "Second floor.", got.Notes)
	assert.Equal(t, "office", got.Group)
	assert.Equal(t, "printer", got.Type)
	assert.True(t, got.Ignored)
}

// The handler reads the checkbox through the same validated form as every
// other field. Unchecked (absent) and an explicit "0" both mean false. "1",
// the value of the template when checked, means true.
func TestDeviceEditIgnoredCheckbox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		form    url.Values
		ignored bool
	}{
		{"checked, the value this app's own form sends", url.Values{"ignored": {"1"}}, true},
		{"unchecked, absent", url.Values{}, false},
		{"explicit 0", url.Values{"ignored": {"0"}}, false},

		// "on" is the value of a bare <input type=checkbox> with no value
		// attribute. The template of this app sends "1". A schema decoder that
		// knows HTML forms also reads "on" as checked.
		{"on", url.Values{"ignored": {"on"}}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, cookies := editor(t)

			rec := patch(t, h, cookies, "/devices/1", tc.form)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tc.ignored, strings.Contains(rec.Body.String(), `name="ignored" value="1" checked`))
		})
	}
}

// A value that is no known spelling of the state of a checkbox fails, as any
// other malformed field does.
func TestDeviceEditRejectsAMalformedCheckbox(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	rec := patch(t, h, cookies, "/devices/1", url.Values{"ignored": {"maybe"}})

	assert.NotEqual(t, http.StatusOK, rec.Code)
}

// The validate tags of the form copy the limits of inventory, because a tag
// cannot name a constant. This test keeps each one at exactly its limit.
func TestDeviceEditHoldsToTheCurationLimits(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	for field, limit := range map[string]int{
		"label": inventory.LabelMaxLength,
		"group": inventory.GroupMaxLength,
		"notes": inventory.NotesMaxLength,
	} {
		rec := patch(t, h, cookies, "/devices/1", url.Values{field: {strings.Repeat("x", limit)}})
		assert.Equal(t, http.StatusOK, rec.Code, "%s at its limit", field)

		rec = patch(t, h, cookies, "/devices/1", url.Values{field: {strings.Repeat("x", limit+1)}})
		assert.NotEqual(t, http.StatusOK, rec.Code, "%s past its limit", field)
	}
}

// html/template renders the fields of the user. So a label with markup is
// escaped, and shows as text.
func TestCurationIsEscaped(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	patch(t, h, cookies, "/devices/1", url.Values{"label": {`<script>alert(1)</script>`}})

	for _, target := range []string{"/devices/1", "/devices", "/devices/1/edit"} {
		t.Run(target, func(t *testing.T) {
			body := requestAs(t, h, cookies, http.MethodGet, target, "").Body.String()

			assert.NotContains(t, body, "<script>alert(1)</script>")
			assert.Contains(t, body, "&lt;script&gt;")
		})
	}
}

// post sends a form as htmx does for a POST, with the signed-in cookies.
func post(t *testing.T, h http.Handler, cookies []*http.Cookie, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	for _, c := range cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// A watch answers with the panel. The panel tells what changed, and has the
// button that undoes it. The list then marks the device, and can show only
// watched devices.
func TestWatchingADevice(t *testing.T) {
	t.Parallel()

	h, cookies := editor(t)

	page := requestAs(t, h, cookies, http.MethodGet, "/devices/1", "").Body.String()
	assert.Contains(t, page, ">Watch</button>")
	assert.Contains(t, page, "Seen recently since")

	rec := post(t, h, cookies, "/devices/1/watch", url.Values{"watched": {"1"}})
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, "Watching printer.local.")
	assert.Contains(t, body, "Yes, told when it goes quiet or comes back")
	assert.Contains(t, body, ">Stop watching</button>")

	list := get(t, h, "/devices?watched=1").Body.String()
	assert.Contains(t, list, "printer.local")
	assert.NotContains(t, list, "nas.local")
	assert.Contains(t, list, `aria-label="Watched"`)

	rec = post(t, h, cookies, "/devices/1/watch", url.Values{"watched": {"0"}})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Stopped watching printer.local.")
}

// A read-only account sees whether a device is watched, but cannot change it.
func TestWatchingIsForWriters(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	rec := post(t, h, nil, "/devices/1/watch", url.Values{"watched": {"1"}})
	assert.NotEqual(t, http.StatusOK, rec.Code)
}
