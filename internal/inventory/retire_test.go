package inventory

import (
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A device that moves to a new address must not keep the old address as
// current. The old address can be silent for longer than the grace window.
// Then a sweep that finds the device elsewhere in the prefix retires it.
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

// Within the grace window, a device with two addresses in the prefix keeps
// both. One silent sweep does not prove that a lease is gone.
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

// A device that answered nowhere in this sweep is offline. A silent sweep tells
// nothing about a move of its lease. So its address does not change, at any
// age.
func TestDoesNotRetireAnAddressOfADeviceItDidNotSee(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	advance(7 * time.Hour)

	res := sweep(t, s, host("192.0.2.20", macB, "nas.local"))

	assert.Zero(t, res.Released)
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, deviceIDByMAC(t, conn, macA)))
}

// This sweep did not probe an address on a different prefix. So its silence
// tells nothing. Only addresses in the swept prefix can retire.
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

// A real host with several addresses answers on every interface in each sweep.
// So both addresses refresh, and neither can retire.
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

// If one interface of a host with several addresses is quiet for longer than
// the window, its address retires. The address that still answers stays.
func TestRetiresTheDeadInterfaceOfAMultiHomedHost(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macA, ""))
	advance(7 * time.Hour)

	res := sweep(t, s, host("192.0.2.10", macA, ""))

	assert.Equal(t, 1, res.Released)
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, deviceIDByMAC(t, conn, macA)))
}

// A router read covers every VLAN at once, with no prefix limit. So it can
// never decide that an address in one is absent.
func TestARouterReadRetiresNothing(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	advance(7 * time.Hour)

	report(t, s, fact("192.0.2.11", macA, "printer.local", true, dbtype.HostnameFromDNS))

	assert.Equal(t, []string{"192.0.2.10", "192.0.2.11"},
		currentIPs(t, conn, deviceIDByMAC(t, conn, macA)))
}
