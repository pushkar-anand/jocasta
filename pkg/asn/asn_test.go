package asn_test

import (
	"net/netip"
	"testing"

	"github.com/pushkar-anand/jocasta/pkg/asn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The public addresses below are long-standing anycast resolvers, chosen
// because the network announcing them has not changed in years and is not
// likely to.
func TestLookupResolvesPublicAddresses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		addr  string
		asn   uint32
		short string
	}{
		{"IPv4", "1.1.1.1", 13335, "Cloudflare"},
		{"IPv4 other network", "8.8.8.8", 15169, "Google"},
		{"IPv6", "2606:4700:4700::1111", 13335, "Cloudflare"},
		{"IPv4-mapped IPv6", "::ffff:8.8.8.8", 15169, "Google"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			org, ok := asn.Lookup(netip.MustParseAddr(tt.addr))
			require.True(t, ok)
			assert.Equal(t, tt.asn, org.ASN)
			assert.Equal(t, tt.short, org.Short)
			assert.NotEmpty(t, org.Name)
		})
	}
}

func TestLookupSkipsAddressesOffThePublicInternet(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{
		"192.0.2.10",     // documentation
		"198.51.100.10",  // documentation
		"203.0.113.10",   // documentation
		"2001:db8::1",    // documentation
		"10.255.255.254", // private
		"172.31.255.254", // private
		"100.64.0.1",     // carrier-grade NAT
		"127.0.0.1",      // loopback
		"169.254.1.1",    // link-local
		"fe80::1",        // link-local
		"224.0.0.1",      // multicast
		"fd00::1",        // unique local
	} {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()

			_, ok := asn.Lookup(netip.MustParseAddr(addr))
			assert.False(t, ok)
			assert.False(t, asn.IsPublic(netip.MustParseAddr(addr)))
		})
	}
}

func TestLookupRejectsTheZeroAddress(t *testing.T) {
	t.Parallel()

	_, ok := asn.Lookup(netip.Addr{})
	assert.False(t, ok)
}
