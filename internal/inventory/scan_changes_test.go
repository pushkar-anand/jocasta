package inventory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// listen has the store record the ids of the scans it reports finished.
func listen(s *Store) *[]int64 {
	var ids []int64

	s.OnScanFinished(func(_ context.Context, id int64) { ids = append(ids, id) })

	return &ids
}

func TestScanListenerHearsEachFinishedScan(t *testing.T) {
	s, _ := newStore(t)
	heard := listen(s)

	first := sweep(t, s, host("192.0.2.10", macA, "host-a"))
	ports := recordPorts(t, s, portScan("192.0.2.10", []uint16{22}, []uint16{22, 80}))

	assert.Equal(t, []int64{first.ScanID, ports.ScanID}, *heard)
}

func TestScanChanges(t *testing.T) {
	s, _ := newStore(t)

	first := sweep(t, s, host("192.0.2.10", macA, "host-a"))

	c, err := s.ScanChanges(t.Context(), first.ScanID)
	require.NoError(t, err)

	assert.Equal(t, dbtype.ScanDiscovery, c.Kind)
	assert.Equal(t, "test-sweep", c.Source)
	assert.Equal(t, prefix, c.Network)
	assert.True(t, c.First, "the first sweep of a network")
	require.NotEmpty(t, c.Events)
	assert.Equal(t, dbtype.EventDeviceDiscovered, c.Events[0].Kind)
	assert.Equal(t, "host-a", c.Events[0].DeviceName)

	second := sweep(t, s, host("192.0.2.10", macA, "host-a"), host("192.0.2.11", macB, "host-b"))

	c, err = s.ScanChanges(t.Context(), second.ScanID)
	require.NoError(t, err)
	assert.False(t, c.First)

	for _, e := range c.Events {
		assert.NotEqual(t, "host-a", e.DeviceName, "only the second scan's changes")
	}
}

func TestScanChangesLeavesOutIgnoredDevices(t *testing.T) {
	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))

	_, err := s.UpdateCuration(t.Context(), deviceIDByMAC(t, conn, macA), Curation{Ignored: true})
	require.NoError(t, err)

	ports := recordPorts(t, s, portScan("192.0.2.10", []uint16{22}, []uint16{22}))

	c, err := s.ScanChanges(t.Context(), ports.ScanID)
	require.NoError(t, err)
	assert.Empty(t, c.Events)
}

func TestScanChangesOfAnUnknownScan(t *testing.T) {
	s, _ := newStore(t)

	_, err := s.ScanChanges(t.Context(), 99)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSpokenName(t *testing.T) {
	assert.Equal(t, "Office printer", spokenName("Office printer", "printer", "Acme", macA, 1))
	assert.Equal(t, "printer", spokenName("", "printer", "Acme", macA, 1))
	assert.Equal(t, "Acme device", spokenName("", "", "Acme", macA, 1))
	assert.Equal(t, macA, spokenName("", "", "", macA, 1))
}
