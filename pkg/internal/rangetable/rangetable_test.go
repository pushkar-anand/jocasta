package rangetable

import (
	"bytes"
	"compress/gzip"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupFindsTheRangeAnAddressFallsIn(t *testing.T) {
	t.Parallel()

	var tb Table[string]
	tb.Add(netip.MustParseAddr("198.51.100.0"), "a")
	tb.Add(netip.MustParseAddr("203.0.113.0"), "b")
	tb.Add(netip.MustParseAddr("2001:db8::"), "c")
	tb.Add(netip.MustParseAddr("2001:db8:1::"), "d")

	for addr, want := range map[string]string{
		"198.51.100.0":   "a",
		"198.51.100.200": "a",
		"203.0.113.7":    "b",
		"203.0.113.255":  "b",
		"2001:db8::5":    "c",
		"2001:db8:1::9":  "d",
	} {
		got, ok := tb.Lookup(netip.MustParseAddr(addr))
		assert.True(t, ok, addr)
		assert.Equal(t, want, got, addr)
	}

	_, ok := tb.Lookup(netip.MustParseAddr("192.0.2.1"))
	assert.False(t, ok, "below every IPv4 start")

	_, ok = tb.Lookup(netip.MustParseAddr("2001:db7::"))
	assert.False(t, ok, "below every IPv6 start")
}

func TestEachLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte("one\ntwo\n"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	var lines []string

	EachLine(buf.Bytes(), func(l string) { lines = append(lines, l) })
	assert.Equal(t, []string{"one", "two"}, lines)

	EachLine([]byte("not gzip"), func(string) { t.Fatal("a corrupt table yields no lines") })
}
