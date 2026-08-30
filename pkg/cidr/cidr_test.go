package cidr_test

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/pushkar-anand/jocasta/pkg/cidr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHosts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
		want   int
		first  string
		last   string
	}{
		{"slash 24 drops network and broadcast", "192.0.2.0/24", 254, "192.0.2.1", "192.0.2.254"},
		{"slash 30 leaves two usable", "10.0.0.0/30", 2, "10.0.0.1", "10.0.0.2"},
		{"slash 31 is point to point", "10.0.0.0/31", 2, "10.0.0.0", "10.0.0.1"},
		{"slash 32 is a single host", "10.0.0.7/32", 1, "10.0.0.7", "10.0.0.7"},
		{"unmasked prefix is masked first", "192.0.2.77/24", 254, "192.0.2.1", "192.0.2.254"},
		{"large prefix", "10.0.0.0/16", 65534, "10.0.0.1", "10.0.255.254"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prefix := netip.MustParsePrefix(tt.prefix)

			seq, err := cidr.Hosts(prefix)
			require.NoError(t, err)

			got := slices.Collect(seq)
			require.Len(t, got, tt.want)

			assert.Equal(t, tt.first, got[0].String())
			assert.Equal(t, tt.last, got[len(got)-1].String())

			count, err := cidr.Count(prefix)
			require.NoError(t, err)
			assert.Equal(t, tt.want, count, "Count disagrees with what Hosts yields")
		})
	}
}

func TestHostsRejects(t *testing.T) {
	t.Parallel()

	t.Run("ipv6", func(t *testing.T) {
		t.Parallel()

		_, err := cidr.Hosts(netip.MustParsePrefix("2001:db8::/120"))
		require.ErrorIs(t, err, cidr.ErrNotIPv4)
	})

	t.Run("zero prefix", func(t *testing.T) {
		t.Parallel()

		_, err := cidr.Hosts(netip.Prefix{})
		require.Error(t, err)
	})

	t.Run("count rejects the same input", func(t *testing.T) {
		t.Parallel()

		_, err := cidr.Count(netip.MustParsePrefix("2001:db8::/120"))
		require.ErrorIs(t, err, cidr.ErrNotIPv4)
	})
}

func TestHostsAreContiguous(t *testing.T) {
	t.Parallel()

	seq, err := cidr.Hosts(netip.MustParsePrefix("198.51.100.0/28"))
	require.NoError(t, err)

	got := slices.Collect(seq)
	require.Len(t, got, 14)

	for i := 1; i < len(got); i++ {
		assert.Equal(t, got[i-1].Next(), got[i], "gap before %s", got[i])
	}
}

// TestHostsIsReiterable covers the retry rounds of a sweep, which range the
// same sequence more than once.
func TestHostsIsReiterable(t *testing.T) {
	t.Parallel()

	seq, err := cidr.Hosts(netip.MustParsePrefix("198.51.100.0/29"))
	require.NoError(t, err)

	assert.Equal(t, slices.Collect(seq), slices.Collect(seq))
}

// TestHostsStopsEarly covers abandoning a sweep part way through.
func TestHostsStopsEarly(t *testing.T) {
	t.Parallel()

	seq, err := cidr.Hosts(netip.MustParsePrefix("10.0.0.0/16"))
	require.NoError(t, err)

	var seen int
	for range seq {
		seen++
		if seen == 3 {
			break
		}
	}

	assert.Equal(t, 3, seen)
}

func BenchmarkHosts(b *testing.B) {
	prefix := netip.MustParsePrefix("192.0.2.0/24")

	b.ReportAllocs()

	for b.Loop() {
		seq, _ := cidr.Hosts(prefix)

		var sink netip.Addr
		for addr := range seq {
			sink = addr
		}

		_ = sink
	}
}
