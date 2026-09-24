package main

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReadsRanges(t *testing.T) {
	t.Parallel()

	spans, err := parse(strings.NewReader("192.0.2.0,192.0.2.127,zz\n198.51.100.0,198.51.100.255,AU\n2001:db8::,2001:db8::ffff,NZ\n"))
	require.NoError(t, err)
	require.Len(t, spans, 3)
	assert.Equal(t, span{start: netip.MustParseAddr("192.0.2.0"), end: netip.MustParseAddr("192.0.2.127"), country: "ZZ"}, spans[0])
	assert.Equal(t, "NZ", spans[2].country)
}

func TestParseRefusesWhatIsNotACountry(t *testing.T) {
	t.Parallel()

	_, err := parse(strings.NewReader("192.0.2.0,192.0.2.127,Australia\n"))
	assert.Error(t, err)
}
