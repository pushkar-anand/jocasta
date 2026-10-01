package inventory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// advertising builds a swept host that advertised services over DNS-SD.
func advertising(ip, mac string, services ...hosts.Service) scanner.Host {
	h := host(ip, mac, "")
	h.Services = services

	return h
}

// tvRemote and tvCast are what an Android TV advertises.
var (
	tvRemote = hosts.Service{Type: "_androidtvremote2._tcp", Instance: "Living Room TV", Port: 6466}
	tvCast   = hosts.Service{
		Type: "_googlecast._tcp", Instance: "Android_0123", Port: 8009,
		Label: "Living Room TV", Model: "Chromecast HD",
	}
)

// A sweep records what a device advertised, and the device page reads it back
// by type and instance.
func TestASweepRecordsAdvertisedServices(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, advertising("192.0.2.10", macA, tvRemote, tvCast))

	d, err := s.Device(t.Context(), deviceIDByMAC(t, conn, macA))
	require.NoError(t, err)
	require.Len(t, d.Services, 2)

	assert.Equal(t, "_androidtvremote2._tcp", d.Services[0].Type)
	assert.Equal(t, "Living Room TV", d.Services[0].Instance)
	assert.Equal(t, uint16(6466), d.Services[0].Port)

	assert.Equal(t, "_googlecast._tcp", d.Services[1].Type)
	assert.Equal(t, "Living Room TV", d.Services[1].Label)
	assert.Equal(t, "Chromecast HD", d.Services[1].Model)
}

// A later sweep updates what changed and keeps first_seen. One that does not
// hear a service leaves its row, since mDNS can drop an answer.
func TestASweepUpdatesAndKeepsServices(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, advertising("192.0.2.10", macA, tvRemote, tvCast))

	id := deviceIDByMAC(t, conn, macA)
	first := queryString(t, conn, `SELECT first_seen FROM device_services WHERE device_id = ? AND type = ?`, id, tvRemote.Type)

	moved := tvRemote
	moved.Port = 6467

	sweep(t, s, advertising("192.0.2.10", macA, moved))

	assert.Equal(t, 2, queryInt(t, conn, `SELECT COUNT(*) FROM device_services WHERE device_id = ?`, id))
	assert.Equal(t, 6467, queryInt(t, conn, `SELECT port FROM device_services WHERE device_id = ? AND type = ?`, id, tvRemote.Type))
	assert.Equal(t, first, queryString(t, conn, `SELECT first_seen FROM device_services WHERE device_id = ? AND type = ?`, id, tvRemote.Type))
}

// A service no sweep has heard within the history window goes with the prune.
func TestPruneDeletesServicesPastRetention(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, advertising("192.0.2.10", macA, tvRemote, tvCast))
	advance(testRetention + time.Hour)
	sweep(t, s, advertising("192.0.2.10", macA, tvCast))

	res, err := s.Prune(t.Context(), Retention{History: testRetention})
	require.NoError(t, err)

	assert.Equal(t, int64(1), res.Services)
	assert.Equal(t, []string{tvCast.Type}, queryStrings(t, conn, `SELECT type FROM device_services`))
}

// The classifier reads what a device advertises, so the Android TV remote
// service makes a TV of a device whose cast port alone says streaming.
func TestServicesClassifyADevice(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, advertising("192.0.2.10", macA, tvRemote, tvCast))

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, string(classify.TV), queryString(t, conn, `SELECT device_class FROM devices WHERE id = ?`, id))

	why, err := s.ExplainClass(t.Context(), id)
	require.NoError(t, err)

	assert.Equal(t, []string{"_androidtvremote2._tcp", "_googlecast._tcp"}, why.Facts.Services)
	assert.Equal(t, []string{"chromecast hd"}, why.Facts.Models)
}
