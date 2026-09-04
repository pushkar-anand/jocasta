package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// patch submits a form the way htmx does.
func patch(t *testing.T, h http.Handler, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestDeviceRowEditServesTheRowAsAForm(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/devices/1/edit")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, `hx-patch="/devices/1/row"`)
	assert.Contains(t, body, `id="device-row-1"`)
	assert.Contains(t, body, `name="label"`)
	assert.Contains(t, body, `name="group"`)

	// Cancelling asks for the row back.
	assert.Contains(t, body, `hx-get="/devices/1/row"`)
}

// A form that shows only two fields still applies all of them, so the ones it
// does not show have to travel with it or the edit would clear them.
func TestDeviceRowFormCarriesTheFieldsItDoesNotShow(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	saved := patch(t, h, "/devices/1", url.Values{
		"label":   {"Office printer"},
		"notes":   {"Second floor."},
		"type":    {"printer"},
		"ignored": {"1"},
	})
	require.Equal(t, http.StatusOK, saved.Code)

	form := get(t, h, "/devices/1/edit").Body.String()

	assert.Contains(t, form, `name="notes" value="Second floor."`)
	assert.Contains(t, form, `name="type" value="printer"`)
	assert.Contains(t, form, `name="ignored" value="1"`)

	// And submitting it unchanged leaves them as they were.
	rec := patch(t, h, "/devices/1/row", url.Values{
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

	rec := patch(t, seeded(t), "/devices/1/row", url.Values{"label": {"Office printer"}})

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	// The row, not the form and not a page.
	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.NotContains(t, body, "hx-patch")
	assert.Contains(t, body, `id="device-row-1"`)
	assert.Contains(t, body, "Office printer")

	// The row a swap puts back has to be able to be edited again.
	assert.Contains(t, body, `hx-get="/devices/1/edit"`)

	// And it still shows what the sweep found, which an edit does not touch.
	assert.Contains(t, body, "192.0.2.10")
	assert.Contains(t, body, macA)
}

func TestUpdateDeviceAnswersWithThePanel(t *testing.T) {
	t.Parallel()

	rec := patch(t, seeded(t), "/devices/1", url.Values{
		"label": {"Office printer"},
		"group": {"office"},
		"notes": {"Hallway."},
	})

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, `id="device-panel"`)

	// The heading is drawn by the layout, outside this swap, so the panel
	// updates it out of band: a new label shows up where the device is named
	// and not only in the field.
	assert.Contains(t, body, `<h1 id="page-title" hx-swap-oob="true">Office printer</h1>`)
	assert.Contains(t, body, "Hallway.")

	// A swapped fragment is the only thing that can report the save.
	assert.Contains(t, body, "Saved.")
}

func TestDevicePageDoesNotClaimToHaveSaved(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, get(t, seeded(t), "/devices/1").Body.String(), "Saved.")
}

func TestCurationSurvivesInTheList(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	patch(t, h, "/devices/1/row", url.Values{"label": {"Office printer"}, "group": {"office"}})

	list := get(t, h, "/devices").Body.String()
	assert.Contains(t, list, "Office printer")
	assert.Contains(t, list, "office")

	// The group is now one the group field can suggest.
	assert.Contains(t, list, `<option value="office">`)

	// And it can be filtered on.
	filtered := get(t, h, "/devices?group=office").Body.String()
	assert.Contains(t, filtered, "Office printer")
	assert.NotContains(t, filtered, "nas.local")
}

func TestIgnoringADeviceHidesItFromTheList(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	patch(t, h, "/devices/1", url.Values{"ignored": {"1"}})

	assert.NotContains(t, get(t, h, "/devices").Body.String(), "printer.local")
	assert.Contains(t, get(t, h, "/devices?ignored=1").Body.String(), "printer.local")
}

func TestEditingUnknownDeviceIsNotFound(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, target := range []string{"/devices/4040/edit", "/devices/4040/row", "/devices/abc/edit"} {
		t.Run(target, func(t *testing.T) {
			assert.Equal(t, http.StatusNotFound, get(t, h, target).Code)
		})
	}

	for _, target := range []string{"/devices/4040", "/devices/4040/row", "/devices/abc/row"} {
		t.Run("patch "+target, func(t *testing.T) {
			rec := patch(t, h, target, url.Values{"label": {"Nothing"}})
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

// The checkbox is read through the same validated form as every other field,
// not by hand: unchecked (absent) and explicitly "0" both mean false, and the
// value the template actually emits when checked, "1", means true.
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

		// "on" is the value a bare <input type=checkbox> submits when it
		// carries no explicit value attribute -- not what this app's own
		// template sends, but a schema decoder that understands HTML forms
		// reads it as checked too.
		{"on", url.Values{"ignored": {"on"}}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := patch(t, seeded(t), "/devices/1", tc.form)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tc.ignored, strings.Contains(rec.Body.String(), `name="ignored" value="1" checked`))
		})
	}
}

// A value that is not any recognised spelling of a checkbox's state fails the
// same way any other malformed field would, rather than being read as
// unchecked.
func TestDeviceEditRejectsAMalformedCheckbox(t *testing.T) {
	t.Parallel()

	rec := patch(t, seeded(t), "/devices/1", url.Values{"ignored": {"maybe"}})

	assert.NotEqual(t, http.StatusOK, rec.Code)
}

// The user's fields are rendered through html/template, so a label carrying
// markup is text and not markup.
func TestCurationIsEscaped(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	patch(t, h, "/devices/1", url.Values{"label": {`<script>alert(1)</script>`}})

	for _, target := range []string{"/devices/1", "/devices", "/devices/1/edit"} {
		t.Run(target, func(t *testing.T) {
			body := get(t, h, target).Body.String()

			assert.NotContains(t, body, "<script>alert(1)</script>")
			assert.Contains(t, body, "&lt;script&gt;")
		})
	}
}
