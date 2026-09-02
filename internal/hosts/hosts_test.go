package hosts

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A hostname is supplied wherever the case under test is not about resolution,
// so the test never reaches the resolver at all.
const suppliedName = "given.lan"

func TestBuildHostParsesTheAddressesItIsGiven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ip   string
		mac  string
		addr netip.Addr
		hw   string
	}{
		{
			name: "IPv4",
			ip:   "192.0.2.10",
			mac:  "00:00:0c:11:22:33",
			addr: netip.MustParseAddr("192.0.2.10"),
			hw:   "00:00:0c:11:22:33",
		},
		{
			name: "IPv6",
			ip:   "2001:db8::1",
			mac:  "00:00:0c:11:22:33",
			addr: netip.MustParseAddr("2001:db8::1"),
			hw:   "00:00:0c:11:22:33",
		},
		{
			// The router renders hardware addresses upper case and the
			// neighbour table lower; both must reach the same address.
			name: "upper case MAC",
			ip:   "192.0.2.10",
			mac:  "00:00:0C:11:22:33",
			addr: netip.MustParseAddr("192.0.2.10"),
			hw:   "00:00:0c:11:22:33",
		},
		{
			name: "hyphen separated MAC",
			ip:   "192.0.2.10",
			mac:  "00-00-0C-11-22-33",
			addr: netip.MustParseAddr("192.0.2.10"),
			hw:   "00:00:0c:11:22:33",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := BuildHost(t.Context(), HostInput{
				IP:        tt.ip,
				MAC:       tt.mac,
				Hostname:  suppliedName,
				Interface: "bridge",
				VLAN:      10,
			})
			require.NoError(t, err)

			assert.Equal(t, tt.addr, h.Address())
			assert.Equal(t, tt.hw, h.HardwareAddress().String())
		})
	}
}

// The raw strings are kept beside the parsed forms, so whatever a source sent
// is still readable after construction.
func TestBuildHostKeepsWhatItWasGiven(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{
		IP:        "192.0.2.10",
		MAC:       "00:00:0C:11:22:33",
		Hostname:  suppliedName,
		Interface: "bridge-vlan20",
		VLAN:      20,
	})
	require.NoError(t, err)

	assert.Equal(t, "192.0.2.10", h.IP)
	assert.Equal(t, "00:00:0C:11:22:33", h.MAC)
	assert.Equal(t, "bridge-vlan20", h.Interface)
	assert.Equal(t, 20, h.VLAN)
}

func TestBuildHostRejectsAMalformedAddress(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.999", Hostname: suppliedName})

	require.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "192.0.2.999")
}

func TestBuildHostRejectsAMalformedMAC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mac  string
	}{
		{"not hexadecimal", "zz:00:0c:11:22:33"},
		{"too short", "00:00:0c"},
		// An incomplete RouterOS ARP row carries no mac-address member at all
		// rather than a short one, but a truncated value must still be refused
		// rather than padded.
		{"five octets", "00:00:0c:11:22"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: tt.mac, Hostname: suppliedName})

			require.Error(t, err)
			assert.Nil(t, h)
			assert.Contains(t, err.Error(), tt.mac)
		})
	}
}

// The all-zero address an incomplete neighbour entry carries parses perfectly
// well and identifies nothing -- and 00:00:00 is a registered prefix, so the
// OUI table attributes it to Xerox. BuildHost does not filter it, which is
// pinned here because it is a trap rather than a decision: a caller that treats
// what comes back as an identity invents a Xerox device for every unresolved
// address. scanner/arp.go rejects the zero address before this point (its
// zeroMAC constant) and every other caller has to do the same.
func TestBuildHostDoesNotRejectTheAllZeroMAC(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "00:00:00:00:00:00", Hostname: suppliedName})

	require.NoError(t, err)
	assert.Equal(t, "00:00:00:00:00:00", h.HardwareAddress().String())
	assert.False(t, h.Randomised())
	assert.Equal(t, "XEROX CORPORATION", h.Vendor(),
		"the zero address resolves to a real vendor; callers must filter it out themselves")
}

func TestBuildHostWithoutAnAddressOrAMAC(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{})
	require.NoError(t, err)

	assert.False(t, h.Address().IsValid())
	assert.Nil(t, h.HardwareAddress())
	assert.Empty(t, h.Hostname())
	assert.Empty(t, h.Vendor())
	assert.Empty(t, h.ShortName())
	assert.False(t, h.Randomised())
}

func TestBuildHostIdentifiesTheVendor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		mac   string
		short string
		full  string
	}{
		{"24-bit assignment", "00:00:0c:11:22:33", "Cisco", "Cisco Systems, Inc"},
		{"lower case input", "3c:22:fb:aa:bb:cc", "Apple", "Apple, Inc."},
		{"upper case input", "3C:22:FB:AA:BB:CC", "Apple", "Apple, Inc."},
		// A 36-bit assignment sits inside a block registered to someone else,
		// so the narrower match must win over its parent.
		{"36-bit assignment", "00:1b:c5:00:00:01", "Converging", "Converging Systems Inc."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: tt.mac, Hostname: suppliedName})
			require.NoError(t, err)

			assert.Equal(t, tt.short, h.ShortName())
			assert.Equal(t, tt.full, h.Vendor())
			assert.False(t, h.Randomised())
		})
	}
}

// A randomised address is generated rather than assigned, so it matches no
// registry and never will. That is worth telling apart from a lookup miss,
// which suggests a stale table instead.
func TestBuildHostMarksARandomisedAddress(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "02:00:5e:10:00:01", Hostname: suppliedName})
	require.NoError(t, err)

	assert.True(t, h.Randomised())
	assert.Empty(t, h.Vendor())
	assert.Empty(t, h.ShortName())
}

// The locally administered bit can be set on an address whose OUI is
// registered, and a device that generated its own address did not become that
// vendor's hardware by colliding with them.
func TestBuildHostPrefersRandomisedOverACollidingRegistration(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "da:a1:19:00:11:22"})
	require.NoError(t, err)

	assert.True(t, h.Randomised())
	assert.Empty(t, h.Vendor())
	assert.Empty(t, h.ShortName())
}

func TestBuildHostLeavesTheVendorEmptyForAnUnregisteredAddress(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "fc:ff:ff:00:00:01", Hostname: suppliedName})
	require.NoError(t, err)

	assert.Empty(t, h.Vendor())
	assert.Empty(t, h.ShortName())
	assert.False(t, h.Randomised(), "a globally administered address is not randomised")
}

func TestBuildHostWithoutAMACHasNoVendor(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", Hostname: suppliedName})
	require.NoError(t, err)

	assert.Nil(t, h.HardwareAddress())
	assert.Empty(t, h.Vendor())
	assert.False(t, h.Randomised())
}

// A supplied name is what a source already knows -- a DHCP lease's host-name --
// and outranks reverse DNS. It must survive construction, and it must not cost
// a lookup: the router's tables run to hundreds of rows and every one of them
// carries a name.
func TestBuildHostKeepsASuppliedHostnameWithoutResolving(t *testing.T) {
	r, calls := countingResolver()
	useResolver(t, r)

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "00:00:0c:11:22:33", Hostname: "lease-name"})
	require.NoError(t, err)

	assert.Equal(t, "lease-name", h.Hostname())
	assert.Zero(t, calls.Load(), "a supplied hostname must not trigger a lookup")
}

func TestBuildHostResolvesWhenNoHostnameIsSupplied(t *testing.T) {
	useResolver(t, stubResolver(t, map[string]string{
		"10.2.0.192.in-addr.arpa.": "printer.lan.",
	}))

	in := HostInput{IP: "192.0.2.10", MAC: "00:00:0c:11:22:33", ResolveName: true}

	h, err := BuildHost(t.Context(), in)
	require.NoError(t, err)

	assert.Equal(t, "printer.lan", h.Hostname())
}

// A resolved name belongs to whoever resolved it, so a caller that has not said
// it can honestly claim one is never handed a name this host's resolver made up.
func TestBuildHostDoesNotResolveUnlessAsked(t *testing.T) {
	r, calls := countingResolver()
	useResolver(t, r)

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "00:00:0c:11:22:33"})
	require.NoError(t, err)

	assert.Empty(t, h.Hostname())
	assert.Zero(t, calls.Load(), "resolution is opt-in")
}

func TestBuildHostLeavesTheHostnameEmptyWhenNothingResolves(t *testing.T) {
	useResolver(t, stubResolver(t, nil))

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10"})
	require.NoError(t, err)

	assert.Empty(t, h.Hostname())
}

// There is nothing to look up without an address, so the resolver is not asked.
func TestBuildHostDoesNotResolveWithoutAnAddress(t *testing.T) {
	r, calls := countingResolver()
	useResolver(t, r)

	h, err := BuildHost(t.Context(), HostInput{MAC: "00:00:0c:11:22:33"})
	require.NoError(t, err)

	assert.Empty(t, h.Hostname())
	assert.Zero(t, calls.Load())
}

// The accessors read the parsed fields, not the raw strings they came from.
func TestHostAccessorsOnTheZeroValue(t *testing.T) {
	t.Parallel()

	var h Host

	assert.False(t, h.Address().IsValid())
	assert.Nil(t, h.HardwareAddress())
	assert.Empty(t, h.Hostname())
	assert.Empty(t, h.Vendor())
	assert.Empty(t, h.ShortName())
	assert.False(t, h.Randomised())
}

// The enriched values live in unexported fields, so encoding a Host without a
// marshaller of its own would quietly emit the half nobody wants.
func TestHostMarshalsItsEnrichedValues(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{
		IP:        "192.0.2.10",
		MAC:       "00:00:0c:11:22:33",
		Hostname:  suppliedName,
		Interface: "eth0",
		VLAN:      20,
	})
	require.NoError(t, err)

	raw, err := json.Marshal(h)
	require.NoError(t, err)

	var got map[string]any

	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, map[string]any{
		"ip":           "192.0.2.10",
		"mac":          "00:00:0c:11:22:33",
		"hostname":     suppliedName,
		"vendor":       "Cisco Systems, Inc",
		"vendor_short": "Cisco",
		"interface":    "eth0",
		"vlan":         float64(20),
	}, got)
}

// Nothing was resolved, so nothing but the address is worth writing.
func TestHostMarshalsAnUnenrichedHost(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10"})
	require.NoError(t, err)

	raw, err := json.Marshal(h)
	require.NoError(t, err)

	assert.JSONEq(t, `{"ip":"192.0.2.10"}`, string(raw))
}
