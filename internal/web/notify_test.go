package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/notify"
)

// ntfyServer answers every request with status and counts them.
func ntfyServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var n atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)

		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	return srv, &n
}

// phone is an ntfy destination named phone, sending to srv.
func phone(t *testing.T, srv *httptest.Server) *notify.Destination {
	t.Helper()

	d, err := notify.NewDestination("phone", notify.Config{Ntfy: &notify.Ntfy{URL: srv.URL + "/jocasta"}})
	require.NoError(t, err)

	return d
}

func notifyHandler(t *testing.T, ds ...*notify.Destination) (http.Handler, *notify.Notifier) {
	t.Helper()

	store, conn := testStoreWithConn(t)
	n := notify.New(conn, store, testLogger(), ds...)

	return newWebHandlerWithAuth(t, store, testAuth(t), WithNotifier(n)), n
}

func TestNotificationsPageForbidsANonAdmin(t *testing.T) {
	t.Parallel()

	srv, sent := ntfyServer(t, http.StatusOK)
	a := testAuth(t)

	_, err := a.CreateUser(t.Context(), "editor", "editor-password-1", dbtype.RoleReadWrite)
	require.NoError(t, err)

	store, conn := testStoreWithConn(t)
	h := newWebHandlerWithAuth(t, store, a, WithNotifier(notify.New(conn, store, testLogger(), phone(t, srv))))
	cookies := signInAs(t, h, "editor", "editor-password-1")

	assert.Equal(t, http.StatusForbidden, requestAs(t, h, cookies, http.MethodGet, "/settings/notifications", "").Code)
	assert.Equal(t, http.StatusForbidden,
		requestAs(t, h, cookies, http.MethodPost, "/settings/notifications/phone/test", "").Code)
	assert.Zero(t, sent.Load())

	body := requestAs(t, h, cookies, http.MethodGet, "/", "").Body.String()
	assert.NotContains(t, body, `href="/settings/notifications"`)
}

func TestNotificationsPageWithoutDestinations(t *testing.T) {
	t.Parallel()

	// No notifier at all, as when no destination is enabled.
	h := empty(t)

	rec := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/notifications", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "No destinations yet")
	assert.Contains(t, body, `href="/settings/notifications" aria-current="page"`)
}

func TestNotificationsPageListsEachDestinationAndItsChoices(t *testing.T) {
	t.Parallel()

	srv, _ := ntfyServer(t, http.StatusOK)
	h, n := notifyHandler(t, phone(t, srv))
	require.NoError(t, n.SetRules(t.Context(), "phone", []dbtype.EventKind{dbtype.EventDeviceDiscovered}))

	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/notifications", "").Body.String()

	assert.Contains(t, body, "phone")
	assert.Contains(t, body, strings.TrimPrefix(srv.URL, "http://"))
	assert.Contains(t, body, `value="DEVICE_DISCOVERED" checked`)
	assert.Contains(t, body, `value="PORT_OPENED">`)
	assert.NotContains(t, body, "last sent", "nothing sent since the server started")

	for _, k := range dbtype.EventKinds() {
		if k == dbtype.EventDeviceEdited {
			assert.NotContains(t, body, `value="`+string(k)+`"`, "an edit is never sent")
			continue
		}

		assert.Contains(t, body, `value="`+string(k)+`"`, "every kind a scan records is offered")
	}
}

func TestSaveNotifications(t *testing.T) {
	t.Parallel()

	srv, _ := ntfyServer(t, http.StatusOK)
	h, n := notifyHandler(t, phone(t, srv))
	cookies := signIn(t, h)

	form := url.Values{"kind": {"PORT_OPENED", "DEVICE_DISCOVERED", "DEVICE_EDITED", "NOT_A_KIND"}}
	rec := follow(t, h, cookies, requestAs(t, h, cookies, http.MethodPost, "/settings/notifications/phone", form.Encode()))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Saved: phone is sent 2 kinds of change.")

	kinds, err := n.Rules(t.Context(), "phone")
	require.NoError(t, err)
	assert.ElementsMatch(t, []dbtype.EventKind{dbtype.EventDeviceDiscovered, dbtype.EventPortOpened}, kinds,
		"a kind the page does not offer is dropped")

	// Nothing ticked clears them.
	rec = requestAs(t, h, cookies, http.MethodPost, "/settings/notifications/phone", url.Values{"kind": {"NOT_A_KIND"}}.Encode())
	assert.Contains(t, follow(t, h, cookies, rec).Body.String(), "Saved: phone is sent nothing.")

	kinds, err = n.Rules(t.Context(), "phone")
	require.NoError(t, err)
	assert.Empty(t, kinds)
}

func TestSaveNotificationsForAnUnknownDestination(t *testing.T) {
	t.Parallel()

	srv, _ := ntfyServer(t, http.StatusOK)
	h, _ := notifyHandler(t, phone(t, srv))

	rec := requestAs(t, h, signIn(t, h), http.MethodPost, "/settings/notifications/tablet",
		url.Values{"kind": {"PORT_OPENED"}}.Encode())
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSendATestNotification(t *testing.T) {
	t.Parallel()

	srv, sent := ntfyServer(t, http.StatusOK)
	h, _ := notifyHandler(t, phone(t, srv))
	cookies := signIn(t, h)

	rec := follow(t, h, cookies, requestAs(t, h, cookies, http.MethodPost, "/settings/notifications/phone/test", ""))

	assert.Equal(t, int32(1), sent.Load())

	body := rec.Body.String()
	assert.Contains(t, body, "Sent a test to phone.")
	assert.Contains(t, body, "last sent")
}

func TestATestNotificationThatFailsSaysWhy(t *testing.T) {
	t.Parallel()

	srv, _ := ntfyServer(t, http.StatusUnauthorized)
	h, _ := notifyHandler(t, phone(t, srv))
	cookies := signIn(t, h)

	rec := follow(t, h, cookies, requestAs(t, h, cookies, http.MethodPost, "/settings/notifications/phone/test", ""))
	host := strings.TrimPrefix(srv.URL, "http://")

	body := rec.Body.String()
	assert.Contains(t, body, "The test did not reach phone ("+host+" answered 401 Unauthorized).")
	assert.Contains(t, body, "Check its url and token under notify in the config file.")
	assert.Contains(t, body, "The last message did not arrive: "+host+" answered 401 Unauthorized.")
}

func TestSavedMessageCountsAgree(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Saved: phone is sent nothing.", savedMessage("phone", 0))
	assert.Equal(t, "Saved: phone is sent 1 kind of change.", savedMessage("phone", 1))
	assert.Equal(t, "Saved: phone is sent 3 kinds of change.", savedMessage("phone", 3))
}
