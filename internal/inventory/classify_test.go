package inventory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// deviceClass reads the classifier's guess and its confidence straight from the
// row, since the reader resolves the user override on top and this wants the
// stored guess itself.
func deviceClass(t *testing.T, s *Store, id int64) (class, confidence string) {
	t.Helper()

	err := s.conn.QueryRowContext(t.Context(),
		`SELECT COALESCE(device_class, ''), COALESCE(device_class_confidence, '')
		 FROM devices WHERE id = ?`, id,
	).Scan(&class, &confidence)
	require.NoError(t, err)

	return class, confidence
}

func TestDiscoveryClassifiesFromTheName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer"))

	id := deviceIDByMAC(t, conn, macA)

	class, confidence := deviceClass(t, s, id)
	assert.Equal(t, "printer", class)
	assert.Equal(t, "medium", confidence)

	// The first guess is part of discovering the device, so it is not logged.
	assert.Equal(t,
		[]dbtype.EventKind{dbtype.EventDeviceDiscovered, dbtype.EventAddressAdded},
		eventKinds(t, conn, id))
}

func TestPortScanCanSettleAnUnclassifiedDevice(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))
	id := deviceIDByMAC(t, conn, macA)

	class, _ := deviceClass(t, s, id)
	require.Empty(t, class, "nothing about host-a says what it is")

	recordPorts(t, s, portScan("192.0.2.10", []uint16{8009}, []uint16{8009, 80}))

	class, confidence := deviceClass(t, s, id)
	assert.Equal(t, "streaming", class)
	assert.Equal(t, "medium", confidence)
}

func TestReclassificationBetweenKnownClassesIsLogged(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer"))
	id := deviceIDByMAC(t, conn, macA)

	// The device is renamed to something the classifier reads as a streaming
	// box. The guess moves printer -> streaming, and that is a change worth a
	// line in the history.
	sweep(t, s, host("192.0.2.10", macA, "nvidia-shield"))

	class, _ := deviceClass(t, s, id)
	assert.Equal(t, "streaming", class)

	kinds := eventKinds(t, conn, id)
	assert.Contains(t, kinds, dbtype.EventDeviceClassified)

	got := queryStrings(t, conn,
		`SELECT old_value || ' -> ' || new_value FROM events WHERE kind = 'DEVICE_CLASSIFIED'`)
	require.Len(t, got, 1)
	assert.Equal(t, "printer -> streaming", got[0])
}

func TestUserTypeOverridesTheGuess(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer"))
	id := deviceIDByMAC(t, conn, macA)

	d, err := s.UpdateCuration(t.Context(), id, Curation{Type: "camera"})
	require.NoError(t, err)

	assert.Equal(t, "camera", string(d.Class), "the user's answer wins")
	assert.Equal(t, "printer", string(d.ClassGuess), "the guess is still shown for what it is")

	// The guess column is untouched: a later scan still maintains it.
	class, _ := deviceClass(t, s, id)
	assert.Equal(t, "printer", class)
}

func TestUnrecognisedUserTypeIsDropped(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer"))
	id := deviceIDByMAC(t, conn, macA)

	d, err := s.UpdateCuration(t.Context(), id, Curation{Type: "toaster"})
	require.NoError(t, err)

	assert.Empty(t, d.Type, "a value that names no class is not stored")
	assert.Equal(t, "printer", string(d.Class), "so the guess still drives the icon")
	assert.Empty(t, queryString(t, conn, `SELECT device_type FROM devices WHERE id = ?`, id))
}
