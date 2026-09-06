package web

import (
	"net/http"
	"net/netip"
	"net/url"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roleClient returns a handler over an inventory of one device, signed in as an
// account with the given role. RoleAdmin uses the seeded test account; the
// others are created for the test.
func roleClient(t *testing.T, role dbtype.UserRole) (http.Handler, []*http.Cookie) {
	t.Helper()

	store := testStore(t)
	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, "printer.local")})
	require.NoError(t, err)

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, store, a)

	if role == dbtype.RoleAdmin {
		return h, signIn(t, h)
	}

	_, err = a.CreateUser(t.Context(), "member", "member-password-1", role)
	require.NoError(t, err)

	return h, signInAs(t, h, "member", "member-password-1")
}

// The curate routes -- the PATCH endpoints and the edit form that only leads to
// them -- turn away an account that may not write, with the same forbidden page
// the Users page gives a non-admin.
func TestCurateRoutesRequireAWriter(t *testing.T) {
	t.Parallel()

	type call struct {
		method, target string
	}

	calls := []call{
		{http.MethodGet, "/devices/1/edit"},
		{http.MethodPatch, "/devices/1"},
		{http.MethodPatch, "/devices/1/row"},
	}

	t.Run("a read user is refused", func(t *testing.T) {
		t.Parallel()

		h, cookies := roleClient(t, dbtype.RoleRead)

		for _, c := range calls {
			body := ""
			if c.method == http.MethodPatch {
				body = url.Values{"label": {"x"}}.Encode()
			}

			rec := requestAs(t, h, cookies, c.method, c.target, body)
			assert.Equal(t, http.StatusForbidden, rec.Code, c.method+" "+c.target)
		}
	})

	t.Run("a read_write user is let through", func(t *testing.T) {
		t.Parallel()

		h, cookies := roleClient(t, dbtype.RoleReadWrite)

		for _, c := range calls {
			body := ""
			if c.method == http.MethodPatch {
				body = url.Values{"label": {"x"}}.Encode()
			}

			rec := requestAs(t, h, cookies, c.method, c.target, body)
			assert.Equal(t, http.StatusOK, rec.Code, c.method+" "+c.target)
		}
	})
}

// A read user browses the inventory but is shown nothing that leads to an edit:
// no Edit button in the list, and the device panel renders the curation values
// read-only rather than as a form.
func TestReadUserSeesNoEditAffordances(t *testing.T) {
	t.Parallel()

	read, readCookies := roleClient(t, dbtype.RoleRead)
	write, writeCookies := roleClient(t, dbtype.RoleReadWrite)

	readList := requestAs(t, read, readCookies, http.MethodGet, "/devices", "").Body.String()
	assert.NotContains(t, readList, `hx-get="/devices/1/edit"`, "no Edit button for a read user")

	writeList := requestAs(t, write, writeCookies, http.MethodGet, "/devices", "").Body.String()
	assert.Contains(t, writeList, `hx-get="/devices/1/edit"`, "a writer still gets the Edit button")

	readPanel := requestAs(t, read, readCookies, http.MethodGet, "/devices/1", "").Body.String()
	assert.NotContains(t, readPanel, `class="curation"`, "no curation form for a read user")
	assert.Contains(t, readPanel, "read-only")

	writePanel := requestAs(t, write, writeCookies, http.MethodGet, "/devices/1", "").Body.String()
	assert.Contains(t, writePanel, `class="curation"`)
}

// A token cannot out-reach the account that mints it: the read_write scope is
// off the form for a read user, and a crafted request for it is refused.
func TestReadUserCannotMintAWriteToken(t *testing.T) {
	t.Parallel()

	read, readCookies := roleClient(t, dbtype.RoleRead)
	write, writeCookies := roleClient(t, dbtype.RoleReadWrite)

	readForm := requestAs(t, read, readCookies, http.MethodGet, "/settings/tokens", "").Body.String()
	assert.NotContains(t, readForm, `value="read_write"`, "a read user is not offered the write scope")

	writeForm := requestAs(t, write, writeCookies, http.MethodGet, "/settings/tokens", "").Body.String()
	assert.Contains(t, writeForm, `value="read_write"`)

	crafted := url.Values{"name": {"escalate"}, "scope": {"read_write"}}.Encode()
	rec := requestAs(t, read, readCookies, http.MethodPost, "/settings/tokens", crafted)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	ok := requestAs(t, write, writeCookies, http.MethodPost, "/settings/tokens", crafted)
	assert.Equal(t, http.StatusSeeOther, ok.Code, "a writer's read_write token still issues")
}
