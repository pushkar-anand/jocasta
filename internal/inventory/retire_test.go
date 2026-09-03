package inventory

import (
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bug in #35: a device that moves to a new address keeps the old one marked
// current. A sweep that finds the device elsewhere in the prefix, once the old
// address has been silent past the grace window, retires it.
func TestRetiresAnAddressADeviceMovedOff(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	advance(7 * time.Hour)

	res := sweep(t, s, host("192.0.2.11", macA, "printer.local"))

	assert.Equal(t, 1, res.Released)

	id := deviceIDByMAC(t, conn, macA)

	assert.Equal(t, []string{"192.0.2.11"}, currentIPs(t, conn, id),
		"the address the device moved off is no longer current")

	d, err := s.Device(t.Context(), id)
	require.NoError(t, err)
	require.Len(t, d.Addresses, 2, "the retired row is kept so its history survives")

	assert.Contains(t, eventKinds(t, conn, id), dbtype.EventAddressReleased)

	old := queryString(t, conn,
		`SELECT old_value FROM events WHERE device_id = ? AND kind = 'ADDRESS_RELEASED'`, id)
	assert.Equal(t, "192.0.2.10", old)
}

// Within the grace window a device holding two addresses in the prefix keeps
// both: one silent sweep is not evidence a lease is gone.
func TestKeepsAnAddressSilentOnlyBrieflyWithinGrace(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	advance(time.Hour)

	res := sweep(t, s, host("192.0.2.11", macA, "printer.local"))

	assert.Zero(t, res.Released)
	assert.Equal(t, []string{"192.0.2.10", "192.0.2.11"},
		currentIPs(t, conn, deviceIDByMAC(t, conn, macA)))
}

// A device that answered nowhere this sweep is offline, not relocated. Nothing
// in a silent sweep says its lease moved, so its address is left alone however
// stale it is.
func TestDoesNotRetireAnAddressOfADeviceItDidNotSee(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	advance(7 * time.Hour)

	res := sweep(t, s, host("192.0.2.20", macB, "nas.local"))

	assert.Zero(t, res.Released)
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, deviceIDByMAC(t, conn, macA)))
}

// An address on another prefix was not probed by this sweep, so its silence
// says nothing. Only addresses inside the swept prefix are eligible.
func TestRetiresOnlyWithinTheSweptPrefix(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	const other = "198.51.100.0/24"

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	sweepPrefix(t, s, other, host("198.51.100.5", macA, "printer.local"))

	advance(7 * time.Hour)

	res := sweepPrefix(t, s, other, host("198.51.100.6", macA, "printer.local"))

	assert.Equal(t, 1, res.Released)
	assert.Equal(t, []string{"192.0.2.10", "198.51.100.6"},
		currentIPs(t, conn, deviceIDByMAC(t, conn, macA)),
		"the address on the unswept prefix is untouched")
}

// A genuinely multi-homed host answers on every interface each sweep, so both
// addresses refresh and neither is ever eligible for retirement.
func TestKeepsBothAddressesOfALiveMultiHomedHost(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	live := func() *Result {
		return sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macA, ""))
	}

	live()
	advance(4 * time.Hour)
	live()
	advance(4 * time.Hour)

	res := live()

	assert.Zero(t, res.Released)
	assert.Equal(t, []string{"192.0.2.10", "192.0.2.11"},
		currentIPs(t, conn, deviceIDByMAC(t, conn, macA)))
}

// One interface of a multi-homed host going quiet past the window retires that
// address and leaves the one still answering.
func TestRetiresTheDeadInterfaceOfAMultiHomedHost(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macA, ""))
	advance(7 * time.Hour)

	res := sweep(t, s, host("192.0.2.10", macA, ""))

	assert.Equal(t, 1, res.Released)
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, deviceIDByMAC(t, conn, macA)))
}

// A router read covers every VLAN at once and bounds itself by no prefix, so it
// can never conclude an address inside one is absent.
func TestARouterReadRetiresNothing(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	advance(7 * time.Hour)

	report(t, s, fact("192.0.2.11", macA, "printer.local", true, dbtype.HostnameFromDNS))

	assert.Equal(t, []string{"192.0.2.10", "192.0.2.11"},
		currentIPs(t, conn, deviceIDByMAC(t, conn, macA)))
}
