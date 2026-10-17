package geo

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
)

// An address that is not on the public internet has no country, even if the
// table gives one.
func TestLookupSkipsWhatIsNotPublic(t *testing.T) {
	t.Parallel()

	for _, a := range []string{"192.0.2.1", "198.51.100.1", "203.0.113.1", "10.0.0.1", "100.64.0.1", "2001:db8::1", "fe80::1"} {
		_, ok := Lookup(netip.MustParseAddr(a))
		assert.False(t, ok, a)
	}
}

// Well-known anycast resolvers are registered in a country. The test does not
// check which country, but the table must give one.
func TestLookupFindsAPublicAddress(t *testing.T) {
	t.Parallel()

	for _, a := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		code, ok := Lookup(netip.MustParseAddr(a))
		assert.True(t, ok, a)
		assert.Len(t, code, 2, a)
	}
}

func TestPackRoundTrips(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "AU", unpack(pack("AU")))
	assert.Equal(t, "ZZ", unpack(pack("ZZ")))
}
