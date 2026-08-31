package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDevices(t *testing.T) {
	t.Parallel()

	res, body := get(t, seeded(t), "/devices")

	require.Equal(t, http.StatusOK, res.StatusCode)
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
			res, body := get(t, h, tc.target)

			require.Equal(t, http.StatusOK, res.StatusCode)
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
			res, body := get(t, h, tc.target)

			// The request parsed and was understood, so it is unprocessable
			// rather than malformed.
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
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

	_, listed := get(t, h, "/devices")
	first, ok := list(t, listed, "devices")[0].(map[string]any)
	require.True(t, ok)

	id, ok := first["id"].(float64)
	require.True(t, ok)

	res, body := get(t, h, "/devices/"+itoa(id))

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, id, body["id"])

	// Only the detail response carries the address history.
	assert.NotEmpty(t, list(t, body, "addresses"))
}

func TestGetDeviceUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	res, body := get(t, seeded(t), "/devices/4040")

	require.Equal(t, http.StatusNotFound, res.StatusCode)
	assert.Equal(t, float64(http.StatusNotFound), body["status"])
}

func TestGetDeviceRejectsNonNumericID(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, target := range []string{"/devices/abc", "/devices/0", "/devices/-1"} {
		t.Run(target, func(t *testing.T) {
			res, _ := get(t, h, target)
			assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		})
	}
}

func TestDeviceEvents(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	_, listed := get(t, h, "/devices")
	first, ok := list(t, listed, "devices")[0].(map[string]any)
	require.True(t, ok)

	id, ok := first["id"].(float64)
	require.True(t, ok)

	res, body := get(t, h, "/devices/"+itoa(id)+"/events")

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.NotEmpty(t, list(t, body, "events"))
}

// The device is looked up first, so history for a device that does not exist is
// a 404 rather than an empty list that reads as "nothing ever happened".
func TestDeviceEventsUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	res, _ := get(t, seeded(t), "/devices/4040/events")
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestStatsAndGroups(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	res, body := get(t, h, "/stats")
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, float64(2), body["total"])
	assert.Equal(t, float64(2), body["online"])
	assert.Equal(t, float64(0), body["offline"])

	res, body = get(t, h, "/groups")
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Nothing has been grouped, so the key is present and holds nothing.
	require.Contains(t, body, "groups")
	assert.Empty(t, body["groups"])
}
