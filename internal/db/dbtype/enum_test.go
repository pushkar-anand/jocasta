package dbtype

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnumValueRenders(t *testing.T) {
	t.Parallel()

	v, err := SourceRouter.Value()
	require.NoError(t, err)
	assert.Equal(t, "ROUTER", v)

	v, err = StatusCancelled.Value()
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", v)

	v, err = EventDevicesMerged.Value()
	require.NoError(t, err)
	assert.Equal(t, "DEVICES_MERGED", v)
}

// The zero value is not a member of any of these sets, and a column reached
// without its constant set is a bug worth a name rather than an empty string
// sitting in the table.
func TestEnumValueRejectsZero(t *testing.T) {
	t.Parallel()

	_, err := SourceKind("").Value()
	require.ErrorContains(t, err, "is not a valid source kind")

	_, err = ScanKind("").Value()
	require.ErrorContains(t, err, "is not a valid scan kind")

	_, err = IdentitySource("").Value()
	require.ErrorContains(t, err, "is not a valid identity source")
}

func TestEnumValueRejectsUnknown(t *testing.T) {
	t.Parallel()

	_, err := ScanStatus("DONE").Value()
	require.ErrorContains(t, err, `"DONE" is not a valid scan status`)

	_, err = EventKind("DEVICE_VANISHED").Value()
	require.ErrorContains(t, err, `"DEVICE_VANISHED" is not a valid event kind`)
}

func TestEnumScanRoundTrips(t *testing.T) {
	t.Parallel()

	var k SourceKind

	require.NoError(t, k.Scan("SWEEP"))
	assert.Equal(t, SourceSweep, k)

	// The driver may hand back either form for a TEXT column.
	require.NoError(t, k.Scan([]byte("MANUAL")))
	assert.Equal(t, SourceManual, k)
}

// Where the schema carries a CHECK it has already refused these; where it does
// not, this is the only thing standing between a hand-edited row and code that
// switches on the value.
func TestEnumScanRejectsUnknown(t *testing.T) {
	t.Parallel()

	var s ScanStatus

	require.ErrorContains(t, s.Scan("FINISHED"), `"FINISHED" is not a valid scan status`)
	assert.Equal(t, ScanStatus(""), s, "a refused value must not be assigned")

	var k EventKind

	require.ErrorContains(t, k.Scan(42), "cannot scan int into a text column")
}

func TestHostnameSourceZeroValueIsNull(t *testing.T) {
	t.Parallel()

	v, err := HostnameSource("").Value()
	require.NoError(t, err)
	assert.Nil(t, v, "a device with no name stores NULL, not an empty string")

	v, err = HostnameFromDNS.Value()
	require.NoError(t, err)
	assert.Equal(t, "DNS", v)
}

func TestHostnameSourceScansNull(t *testing.T) {
	t.Parallel()

	s := HostnameFromDNS

	require.NoError(t, s.Scan(nil))
	assert.Equal(t, HostnameSource(""), s)

	require.ErrorContains(t, s.Scan("MDNS"), `"MDNS" is not a valid hostname source`)
}

func TestEnumValid(t *testing.T) {
	t.Parallel()

	assert.True(t, SourceDNS.Valid())
	assert.False(t, SourceKind("BOGUS").Valid())
	assert.True(t, IdentityMAC.Valid())
	assert.False(t, IdentitySource("HOSTNAME").Valid())
	assert.True(t, ScanImport.Valid())
	assert.True(t, StatusRunning.Valid())
	assert.True(t, EventAddressAdded.Valid())
	assert.True(t, HostnameFromDNS.Valid())
}
