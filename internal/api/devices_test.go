package api

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDevices(t *testing.T) {
	t.Parallel()

	status, _, body := get(t, seeded(t), "/devices")

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(2), body["count"])

	devices := list(t, body, "devices")
	require.Len(t, devices, 2)

	device, ok := devices[0].(map[string]any)
	require.True(t, ok)

	// The flattened view is what reaches the wire, so an absent column is left
	// out entirely. The raw row would send {"String":"","Valid":false}.
	assert.Contains(t, device, "id")
	assert.Contains(t, device, "online")
	assert.Contains(t, device, "current_addresses")
	assert.NotContains(t, device, "label")
	assert.NotContains(t, device, "notes")
}

func TestListDevicesFilters(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	tests := []struct {
		name   string
		target string
		want   int
	}{
		{"unfiltered", "/devices", 2},
		{"by hostname", "/devices?q=nas", 1},
		{"by address", "/devices?q=192.0.2.10", 1},
		{"matching nothing", "/devices?q=absent", 0},
		{"online", "/devices?status=online", 2},
		{"offline", "/devices?status=offline", 0},
		{"sorted", "/devices?sort=address", 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, _, body := get(t, h, tc.target)

			require.Equal(t, http.StatusOK, status)
			assert.Equal(t, float64(tc.want), body["count"])
			assert.Len(t, list(t, body, "devices"), tc.want)
		})
	}
}

// A filter that cannot be honoured is reported, since returning the unfiltered
// list would look like the filter matched everything.
func TestListDevicesRejectsUnknownFilterValues(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	tests := []struct{ target, field string }{
		{"/devices?status=onlin", "status"},
		{"/devices?sort=vendor", "sort"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			status, _, body := get(t, h, tc.target)

			// The request parsed and was understood, so it is unprocessable
			// (422). A malformed request would be a 400.
			require.Equal(t, http.StatusUnprocessableEntity, status)
			assert.Equal(t, float64(http.StatusUnprocessableEntity), body["status"])

			// The problem names the parameter it is about, so a client is told
			// which one to fix.
			assert.Contains(t, problemContext(t, body), tc.field)
		})
	}
}

func TestGetDevice(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	_, _, listed := get(t, h, "/devices")
	first, ok := list(t, listed, "devices")[0].(map[string]any)
	require.True(t, ok)

	id, ok := first["id"].(float64)
	require.True(t, ok)

	u := fmt.Sprintf("/devices/%d", int(id))

	status, _, body := get(t, h, u)

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, id, body["id"])

	// Only the detail response carries the address history.
	assert.NotEmpty(t, list(t, body, "addresses"))
}

// The detail response carries the open ports a scan has recorded; the list does
// not.
func TestGetDeviceCarriesOpenPorts(t *testing.T) {
	t.Parallel()

	store := testStore(t)

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, "printer.local")})
	require.NoError(t, err)

	_, err = store.RecordPorts(t.Context(), "test-sweep", []scanner.PortScan{
		{Addr: netip.MustParseAddr("192.0.2.10"), Open: []uint16{22}, Scanned: []uint16{22, 80}},
	})
	require.NoError(t, err)

	h := NewHandler(testLogger(), testReader(t), store, testJSONWriter())

	status, _, body := get(t, h, "/devices/1")
	require.Equal(t, http.StatusOK, status)

	ports := list(t, body, "ports")
	require.Len(t, ports, 1)

	port, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(22), port["port"])
	assert.Equal(t, "ssh", port["service"])
	assert.Equal(t, "open", port["state"])

	// The list carries no ports.
	_, _, listed := get(t, h, "/devices")
	first, ok := list(t, listed, "devices")[0].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, first, "ports")
}

// A device that advertised services over DNS-SD carries them, and the list
// leaves them out as it does ports.
func TestGetDeviceCarriesServices(t *testing.T) {
	t.Parallel()

	store := testStore(t)

	tv := host("192.0.2.10", macA, "")
	tv.Services = []hosts.Service{{Type: "_googlecast._tcp", Instance: "Android_0123", Port: 8009, Label: "Living Room TV"}}

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), []scanner.Host{tv})
	require.NoError(t, err)

	h := NewHandler(testLogger(), testReader(t), store, testJSONWriter())

	status, _, body := get(t, h, "/devices/1")
	require.Equal(t, http.StatusOK, status)

	services := list(t, body, "services")
	require.Len(t, services, 1)

	sv, ok := services[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "_googlecast._tcp", sv["type"])
	assert.Equal(t, float64(8009), sv["port"])
	assert.Equal(t, "Living Room TV", sv["label"])

	_, _, listed := get(t, h, "/devices")
	first, ok := list(t, listed, "devices")[0].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, first, "services")
}

func TestGetDeviceUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	status, _, body := get(t, seeded(t), "/devices/4040")

	require.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, float64(http.StatusNotFound), body["status"])
}

func TestGetDeviceRejectsNonNumericID(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, target := range []string{"/devices/abc", "/devices/0", "/devices/-1"} {
		t.Run(target, func(t *testing.T) {
			status, _, _ := get(t, h, target)
			assert.Equal(t, http.StatusBadRequest, status)
		})
	}
}

func TestDeviceEvents(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	_, _, listed := get(t, h, "/devices")
	first, ok := list(t, listed, "devices")[0].(map[string]any)
	require.True(t, ok)

	id, ok := first["id"].(float64)
	require.True(t, ok)

	u := fmt.Sprintf("/devices/%d/events", int(id))

	status, _, body := get(t, h, u)

	require.Equal(t, http.StatusOK, status)
	assert.NotEmpty(t, list(t, body, "events"))
}

// The device is looked up first, so history for a device that does not exist is
// a 404. An empty list would read as "nothing ever happened".
func TestDeviceEventsUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	status, _, _ := get(t, seeded(t), "/devices/4040/events")
	assert.Equal(t, http.StatusNotFound, status)
}

func TestStatsAndGroups(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	status, _, body := get(t, h, "/stats")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(2), body["total"])
	assert.Equal(t, float64(2), body["online"])
	assert.Equal(t, float64(0), body["offline"])

	status, _, body = get(t, h, "/groups")
	require.Equal(t, http.StatusOK, status)

	// Nothing has been grouped, so the key is present and holds nothing.
	require.Contains(t, body, "groups")
	assert.Empty(t, body["groups"])
}

func TestUpdateDevice(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	status, _, body := patchJSON(t, h, "/devices/1", `{
		"label": "Office printer",
		"notes": "Second floor.",
		"group": "office",
		"type": "printer"
	}`)

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Office printer", body["label"])
	assert.Equal(t, "office", body["group"])
	assert.Equal(t, "printer", body["type"])
	assert.Equal(t, false, body["ignored"])

	// The device carries its addresses, as it does from every other read.
	assert.NotEmpty(t, body["current_addresses"])

	// What the sweep found is untouched.
	assert.Equal(t, macA, body["mac"])

	// The change is stored as well as returned.
	_, _, reread := get(t, h, "/devices/1")
	assert.Equal(t, "Office printer", reread["label"])
}

// A field left out of the body keeps its value.
func TestUpdateDeviceKeepsOmittedFields(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	patchJSON(t, h, "/devices/1", `{
		"label": "Office printer",
		"notes": "Second floor.",
		"group": "office",
		"type": "printer",
		"ignored": true
	}`)

	_, _, body := patchJSON(t, h, "/devices/1", `{"label": "Hallway printer"}`)
	assert.Equal(t, "Hallway printer", body["label"])
	assert.Equal(t, "Second floor.", body["notes"])
	assert.Equal(t, "office", body["group"])
	assert.Equal(t, "printer", body["type"])
	assert.Equal(t, true, body["ignored"])
}

// A field sent empty is cleared, and only that field.
func TestUpdateDeviceClearsAFieldSentEmpty(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	patchJSON(t, h, "/devices/1", `{"label": "Office printer", "notes": "Second floor.", "group": "office"}`)

	_, _, body := patchJSON(t, h, "/devices/1", `{"notes": ""}`)
	assert.NotContains(t, body, "notes")
	assert.Equal(t, "Office printer", body["label"])
	assert.Equal(t, "office", body["group"])
}

func TestUpdateDeviceRecordsTheEdit(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	patchJSON(t, h, "/devices/1", `{"label": "Office printer"}`)

	_, _, body := get(t, h, "/devices/1/events")

	var edits int

	for _, item := range list(t, body, "events") {
		event, ok := item.(map[string]any)
		require.True(t, ok)

		if event["kind"] == "DEVICE_EDITED" {
			edits++

			assert.Equal(t, "label", event["detail"])
			assert.Equal(t, "Office printer", event["new_value"])
		}
	}

	assert.Equal(t, 1, edits, "one event for the one field that moved")
}

func TestUpdateDeviceUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	status, _, _ := patchJSON(t, seeded(t), "/devices/4040", `{"label": "Nothing"}`)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestUpdateDeviceRejectsAMalformedBody(t *testing.T) {
	t.Parallel()

	status, _, _ := patchJSON(t, seeded(t), "/devices/1", `{"label":`)
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestUpdateDeviceRejectsInvalidType(t *testing.T) {
	t.Parallel()

	status, _, body := patchJSON(t, seeded(t), "/devices/1", `{"type": "hovercraft"}`)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, problemContext(t, body), "type")
}

// The body's validate tags spell out inventory's limits, since a tag cannot
// name a constant; a field exactly at its limit is accepted.
func TestUpdateDeviceAcceptsFieldsAtTheirLimit(t *testing.T) {
	t.Parallel()

	body := fmt.Sprintf(`{"label": %q, "group": %q, "notes": %q}`,
		strings.Repeat("x", inventory.LabelMaxLength),
		strings.Repeat("x", inventory.GroupMaxLength),
		strings.Repeat("x", inventory.NotesMaxLength))
	status, _, _ := patchJSON(t, seeded(t), "/devices/1", body)

	require.Equal(t, http.StatusOK, status)
}

func TestUpdateDeviceRejectsOverlongLabel(t *testing.T) {
	t.Parallel()

	label := strings.Repeat("x", inventory.LabelMaxLength+1)
	status, _, body := patchJSON(t, seeded(t), "/devices/1", fmt.Sprintf(`{"label": %q}`, label))

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, problemContext(t, body), "label")
}

func TestUpdateDeviceRejectsOverlongNotes(t *testing.T) {
	t.Parallel()

	notes := strings.Repeat("x", inventory.NotesMaxLength+1)
	status, _, body := patchJSON(t, seeded(t), "/devices/1", fmt.Sprintf(`{"notes": %q}`, notes))

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, problemContext(t, body), "notes")
}

func TestUpdateDeviceRejectsOverlongGroup(t *testing.T) {
	t.Parallel()

	group := strings.Repeat("x", inventory.GroupMaxLength+1)
	status, _, body := patchJSON(t, seeded(t), "/devices/1", fmt.Sprintf(`{"group": %q}`, group))

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, problemContext(t, body), "group")
}

func TestUpdateDeviceAcceptsValidType(t *testing.T) {
	t.Parallel()

	status, _, body := patchJSON(t, seeded(t), "/devices/1", `{"type": "printer"}`)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "printer", body["type"])
}

func TestListDevicesAcceptsSortType(t *testing.T) {
	t.Parallel()

	status, _, body := get(t, seeded(t), "/devices?sort=type")

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(2), body["count"])
}

func TestDeviceTraffic(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	status, _, body := get(t, h, "/devices/1/traffic?days=7")
	require.Equal(t, http.StatusOK, status)

	// Nothing collects traffic in this test, and the answer says so, which
	// tells it apart from a silent device.
	assert.Equal(t, false, body["recorded"])
	assert.Empty(t, list(t, body, "local"))
	assert.Empty(t, list(t, body, "internet"))

	status, _, _ = get(t, h, "/devices/4040/traffic")
	assert.Equal(t, http.StatusNotFound, status)

	status, _, _ = get(t, h, "/devices/1/traffic?days=91")
	assert.Equal(t, http.StatusUnprocessableEntity, status)
}

func TestWatchDevice(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	status, _, body := patchJSON(t, h, "/devices/1/watch", `{"watched": true}`)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, body["watched"])

	// Curation replaces every field it carries, and watching is not one.
	_, _, body = patchJSON(t, h, "/devices/1", `{"label": "Office printer"}`)
	assert.Equal(t, true, body["watched"])

	_, _, listed := get(t, h, "/devices?watched=true")
	assert.Len(t, list(t, listed, "devices"), 1)

	// A body that does not say which way is refused, since reading it as
	// false would stop watching.
	status, _, _ = patchJSON(t, h, "/devices/1/watch", `{}`)
	assert.Equal(t, http.StatusUnprocessableEntity, status)

	status, _, _ = patchJSON(t, h, "/devices/999/watch", `{"watched": true}`)
	assert.Equal(t, http.StatusNotFound, status)
}
