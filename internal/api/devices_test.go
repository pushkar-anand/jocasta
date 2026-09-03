package api

import (
	"net/http"
	"net/netip"
	"testing"

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

	// The flattened view is what reaches the wire: an absent column is left out
	// entirely rather than arriving as {"String":"","Valid":false}.
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
			// rather than malformed.
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

	status, _, body := get(t, h, "/devices/"+itoa(id))

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

	h := NewHandler(testLogger(), testReader(t), store)

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

	status, _, body := get(t, h, "/devices/"+itoa(id)+"/events")

	require.Equal(t, http.StatusOK, status)
	assert.NotEmpty(t, list(t, body, "events"))
}

// The device is looked up first, so history for a device that does not exist is
// a 404 rather than an empty list that reads as "nothing ever happened".
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

	// And what the sweep found is untouched: an address is not something to
	// correct by hand.
	assert.Equal(t, macA, body["mac"])

	// The change is stored, not only returned.
	_, _, reread := get(t, h, "/devices/1")
	assert.Equal(t, "Office printer", reread["label"])
}

// Every field is applied, so one left out of the body clears what was there.
func TestUpdateDeviceClearsOmittedFields(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	patchJSON(t, h, "/devices/1", `{"label": "Office printer", "group": "office"}`)

	_, _, body := patchJSON(t, h, "/devices/1", `{"label": "Office printer"}`)
	assert.Equal(t, "Office printer", body["label"])
	assert.NotContains(t, body, "group")
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
