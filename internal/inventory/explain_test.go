package inventory

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

func TestExplainClassGivesTheFactsAndTheRules(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer"))
	recordPorts(t, s, portScan("192.0.2.10", []uint16{515}, []uint16{515, 80}))

	id := deviceIDByMAC(t, conn, macA)

	got, err := s.ExplainClass(t.Context(), id)
	require.NoError(t, err)

	assert.Equal(t, classify.Printer, got.Class)
	assert.Equal(t, classify.Printer, got.Guess)
	assert.Equal(t, classify.Medium, got.Confidence)
	assert.False(t, got.Overridden)
	assert.Empty(t, got.Type)

	class, confidence := deviceClass(t, s, id)
	assert.Equal(t, class, string(got.Guess), "the explanation is the case for the recorded guess")
	assert.Equal(t, confidence, string(got.Confidence))

	assert.Equal(t, "printer", got.Facts.Hostname)
	assert.Equal(t, dbtype.HostnameFromDNS, got.Facts.HostnameSource)
	assert.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.10")}, got.Facts.Addresses)
	assert.Equal(t, []uint16{515}, got.Facts.OpenPorts)

	require.NotNil(t, got.Rule)
	assert.Equal(t, classify.Printer, got.Rule.Class)
	assert.Equal(t, "the name is a printer model", got.Rule.Reason)

	require.Len(t, got.OtherRules, 1)
	assert.Equal(t, classify.Printer, got.OtherRules[0].Class)
	assert.Equal(t, "port 515 (LPD print service)", got.OtherRules[0].Reason)
}

func TestExplainClassKeepsTheGuessUnderTheOwnersType(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer"))
	id := deviceIDByMAC(t, conn, macA)

	_, err := s.UpdateCuration(t.Context(), id, Curation{Type: string(classify.Camera)})
	require.NoError(t, err)

	got, err := s.ExplainClass(t.Context(), id)
	require.NoError(t, err)

	assert.Equal(t, classify.Camera, got.Class, "the owner's type wins")
	assert.Equal(t, classify.Camera, got.Type)
	assert.True(t, got.Overridden)
	assert.Equal(t, classify.Printer, got.Guess)
	require.NotNil(t, got.Rule)
	assert.Equal(t, classify.Printer, got.Rule.Class)
}

func TestExplainClassWithNoMatchingRule(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))
	id := deviceIDByMAC(t, conn, macA)

	got, err := s.ExplainClass(t.Context(), id)
	require.NoError(t, err)

	assert.Equal(t, classify.Unknown, got.Class)
	assert.Equal(t, classify.NoConfidence, got.Confidence)
	assert.Nil(t, got.Rule)
	assert.NotNil(t, got.OtherRules, "an empty list is still a list")
	assert.Empty(t, got.OtherRules)
	assert.NotNil(t, got.Facts.OpenPorts, "an empty list is still a list")
}

func TestExplainClassOfAMissingDevice(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	_, err := s.ExplainClass(t.Context(), 9999)
	require.ErrorIs(t, err, ErrNotFound)
}
