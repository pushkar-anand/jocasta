package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

func TestPortRows(t *testing.T) {
	t.Parallel()

	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)

	ports := []*inventory.Port{
		{Number: 8009, State: dbtype.PortOpen, FirstSeen: late},
		{Number: 22, Service: "ssh", State: dbtype.PortClosed, FirstSeen: early},
		{Number: 8008, Service: "http-alt", State: dbtype.PortOpen, FirstSeen: late},
	}

	services := []*inventory.Service{
		{Type: "_googlecast._tcp", Instance: "Android_0123", Port: 8009, Label: "Living Room TV", Model: "Chromecast HD", FirstSeen: early},
		{Type: "_matterc._udp", Instance: "0123", Port: 5540, FirstSeen: late},
		{Type: "_example._tcp", Instance: "No port", FirstSeen: late},
	}

	rows := portRows(ports, services)
	require.Len(t, rows, 5)

	// Open or advertised first, by number, with the portless service last
	// among them; the port only a scan saw, now closed, at the end.
	got := make([]uint16, len(rows))
	for i, r := range rows {
		got[i] = r.Number
	}

	assert.Equal(t, []uint16{5540, 8008, 8009, 0, 22}, got)

	cast := rows[2]
	assert.True(t, cast.Scanned)
	assert.True(t, cast.Open)
	assert.Equal(t, early, cast.FirstSeen, "the earlier of the scan and the advertisement")
	assert.Equal(t, []advertised{{Name: "Google Cast", Type: "_googlecast._tcp", Instance: "Living Room TV", Model: "Chromecast HD"}}, cast.Advertised)

	matter := rows[0]
	assert.True(t, matter.UDP)
	assert.False(t, matter.Scanned)
	assert.Equal(t, "Matter setup", matter.Advertised[0].Name)

	assert.Empty(t, rows[3].Advertised[0].Name, "a type with no friendly name")
}
