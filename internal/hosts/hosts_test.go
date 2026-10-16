package hosts

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each case that is not about resolution gives a hostname. So the test never
// reaches the resolver.
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
			// The router renders hardware addresses in upper case, and the
			// neighbour table in lower case. Both must give the same address.
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

// The raw strings stay next to the parsed forms. So what a source sent is still
// readable after construction.
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
		// An incomplete RouterOS ARP row has no mac-address member. But a cut
		// value must still fail. Padding would make up octets.
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

// An incomplete neighbour entry has the all-zero address. It parses, but it
// identifies nothing. 00:00:00 is a registered prefix, so the OUI table gives
// it to Xerox. BuildHost does not filter it, and this test records that trap.
// If a caller uses the result as an identity, it makes up a Xerox device for
// every unresolved address. So callers first pass the address through
// [CanonicalMAC], which removes it.
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
		// A 36-bit assignment is in a block that belongs to a different owner.
		// So the narrower match must win over its parent.
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

// The device makes up a random address. So it matches no registry, and never
// will. Keep this separate from a lookup miss, which suggests an old table.
func TestBuildHostMarksARandomisedAddress(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "02:00:5e:10:00:01", Hostname: suppliedName})
	require.NoError(t, err)

	assert.True(t, h.Randomised())
	assert.Empty(t, h.Vendor())
	assert.Empty(t, h.ShortName())
}

// A vendor can use a locally administered prefix without a registration, and
// the table has such prefixes. Such a device is hardware of that vendor. So
// the two answers are separate: locally administered, and still known.
func TestBuildHostNamesTheVendorOfALocallyAdministeredPrefix(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "da:a1:19:00:11:22"})
	require.NoError(t, err)

	assert.True(t, h.Randomised())
	assert.Equal(t, "Google", h.ShortName())
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

// A given name, such as the host-name of a DHCP lease, is what a source already
// knows. It ranks above reverse DNS. It must stay after construction, and it
// must not cause a lookup. The tables of the router have hundreds of rows, and
// each row has a name.
func TestBuildHostKeepsASuppliedHostnameWithoutResolving(t *testing.T) {
	r, calls := countingResolver()
	useResolver(t, r)

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "00:00:0c:11:22:33", Hostname: "lease-name"})
	require.NoError(t, err)

	assert.Equal(t, "lease-name", h.Hostname())
	assert.Zero(t, calls.Load(), "a supplied hostname must not trigger a lookup")
}

// A router or DHCP server passes on any name that a client asked for. So
// BuildHost checks a name that it reports before it keeps it.
func TestBuildHostDropsAnUnusableName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "a space", in: "Living Room TV", want: "Living Room TV"},
		{name: "surrounding whitespace", in: "  host-a\n", want: "host-a"},
		{name: "a newline", in: "host-a\nhost-b"},
		{name: "a right-to-left override", in: "host-a\u202egnp.exe"},
		{name: "over 253 bytes", in: strings.Repeat("a", 300)},
		{name: "invalid UTF-8", in: "host-a\xff"},
		{name: "whitespace alone", in: " \t "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "00:00:5e:00:53:01", Hostname: tt.in})
			require.NoError(t, err)

			assert.Equal(t, tt.want, h.Hostname())
			assert.Equal(t, tt.want, h.Named(tt.in).Hostname())
		})
	}
}

func TestBuildHostResolvesWhenNoHostnameIsSupplied(t *testing.T) {
	useResolver(t, stubResolver(t, map[string][]string{
		"10.2.0.192.in-addr.arpa.": {"printer.lan."},
	}))

	in := HostInput{IP: "192.0.2.10", MAC: "00:00:0c:11:22:33", ResolveName: true}

	h, err := BuildHost(t.Context(), in)
	require.NoError(t, err)

	assert.Equal(t, "printer.lan", h.Hostname())
	assert.Equal(t, []string{"printer.lan"}, h.ReverseNames())
}

// If the address of a host has several PTR records, the host gets the first
// name in sorted order. It keeps all names, so the device page can list the
// others.
func TestBuildHostKeepsEveryReverseName(t *testing.T) {
	useResolver(t, stubResolver(t, map[string][]string{
		"10.2.0.192.in-addr.arpa.": {"host-a.example.com.", "dns2.example.com."},
	}))

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", ResolveName: true})
	require.NoError(t, err)

	assert.Equal(t, "dns2.example.com", h.Hostname())
	assert.Equal(t, []string{"dns2.example.com", "host-a.example.com"}, h.ReverseNames())
}

// A resolved name belongs to the source that resolved it. So if a caller did
// not say that it can claim a name, it never gets a name from this host's
// resolver.
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

// Without an address, there is nothing to look up. So BuildHost does not ask
// the resolver.
func TestBuildHostDoesNotResolveWithoutAnAddress(t *testing.T) {
	r, calls := countingResolver()
	useResolver(t, r)

	h, err := BuildHost(t.Context(), HostInput{MAC: "00:00:0c:11:22:33"})
	require.NoError(t, err)

	assert.Empty(t, h.Hostname())
	assert.Zero(t, calls.Load())
}

// The accessors read the parsed fields. These are empty on the zero value.
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

// The enriched values are in unexported fields. Without its own marshaller, a
// Host would encode only the less useful half, with no warning.
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

// Nothing resolved, so only the address is worth a write.
func TestHostMarshalsAnUnenrichedHost(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10"})
	require.NoError(t, err)

	raw, err := json.Marshal(h)
	require.NoError(t, err)

	assert.JSONEq(t, `{"ip":"192.0.2.10"}`, string(raw))
}

func TestCanonicalMAC(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{in: "00-00-5E-00-53-01", want: "00:00:5e:00:53:01", ok: true},
		{in: "00:00:5e:00:53:01", want: "00:00:5e:00:53:01", ok: true},
		{in: "", want: "", ok: true},
		{in: "00:00:00:00:00:00", want: "", ok: true},
		{in: "not-a-mac", want: "", ok: false},
	} {
		got, ok := CanonicalMAC(tc.in)
		assert.Equal(t, tc.want, got, tc.in)
		assert.Equal(t, tc.ok, ok, tc.in)
	}
}

func TestNamedCopiesTheHostWithTheName(t *testing.T) {
	t.Parallel()

	h, err := BuildHost(t.Context(), HostInput{IP: "192.0.2.10", MAC: "00:00:0c:11:22:33", Hostname: suppliedName})
	require.NoError(t, err)

	named := h.Named("tv.local")

	assert.Equal(t, "tv.local", named.Hostname())
	assert.Equal(t, h.Address(), named.Address())
	assert.Equal(t, h.Vendor(), named.Vendor())

	// The original host keeps its own name.
	assert.Equal(t, suppliedName, h.Hostname())
}
