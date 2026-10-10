package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/config"
)

// A router read over plain HTTP gets the password in the clear, so startup
// warns about each one that has ssl off, of either kind.
func TestRouterSourcesWarnAboutPlainHTTP(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Plugins.RouterOS = map[string]config.RouterOS{
		"gateway": {Source: config.Source{Enabled: true}, HTTPLogin: config.HTTPLogin{Host: "192.0.2.1"}},
		"rack":    {Source: config.Source{Enabled: true}, HTTPLogin: config.HTTPLogin{Host: "198.51.100.1", SSL: true}},
		"spare":   {Source: config.Source{Enabled: false}, HTTPLogin: config.HTTPLogin{Host: "203.0.113.1"}},
	}
	cfg.Plugins.OpenWrt = map[string]config.OpenWrt{
		"ap": {Source: config.Source{Enabled: true}, HTTPLogin: config.HTTPLogin{Host: "192.0.2.2"}},
	}

	var buf bytes.Buffer

	_, err := routerSources(t.Context(), cfg, slog.New(slog.NewJSONHandler(&buf, nil)))
	require.NoError(t, err)

	var warned []string

	for line := range strings.Lines(buf.String()) {
		var rec struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
			Src   string `json:"src"`
		}

		require.NoError(t, json.Unmarshal([]byte(line), &rec))

		if rec.Level == slog.LevelWarn.String() {
			assert.Equal(t,
				"router is reached over plain HTTP, so its password crosses the network unencrypted. Set ssl: true",
				rec.Msg)

			warned = append(warned, rec.Src)
		}
	}

	assert.Equal(t, []string{"ap", "gateway"}, warned)
}

func TestTrafficReportersBuildsOnlyEnabledInstances(t *testing.T) {
	t.Parallel()

	var cfg config.Config

	cfg.Plugins.NetFlow = map[string]config.NetFlow{
		"gateway": {Enabled: true, Exporters: []string{"192.0.2.1"}},
		"spare":   {Enabled: false, Exporters: []string{"198.51.100.1"}},
	}

	got, err := trafficReporters(t.Context(), &cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "netflow:gateway", got[0].Name())
}

// An enabled instance that names no exporters would accept datagrams from
// anyone, so it stops startup.
func TestTrafficReportersRefusesAnInstanceWithoutExporters(t *testing.T) {
	t.Parallel()

	var cfg config.Config

	cfg.Plugins.NetFlow = map[string]config.NetFlow{"gateway": {Enabled: true}}

	_, err := trafficReporters(t.Context(), &cfg, slog.New(slog.DiscardHandler))
	require.Error(t, err)
}

// A home country is taken in any case and checked against the map, so a typo
// fails startup.
func TestHomeCountryIsCheckedAgainstTheMap(t *testing.T) {
	t.Parallel()

	got, err := homeCountry(" au ")
	require.NoError(t, err)
	assert.Equal(t, "AU", got)

	got, err = homeCountry("")
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = homeCountry("XX")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location.country")
}

// Trusted proxies mix addresses and CIDRs. A typo fails startup.
func TestTrustedProxiesAreParsed(t *testing.T) {
	t.Parallel()

	got, err := trustedProxies([]string{"192.0.2.1", "198.51.100.0/24", "2001:db8::/32"})
	require.NoError(t, err)
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.0.2.1/32"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}, got)

	got, err = trustedProxies(nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = trustedProxies([]string{"proxy.example"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server.trusted_proxies")
}

// A time zone is looked up by its IANA name, so a typo fails startup.
func TestTimezoneIsLookedUpByName(t *testing.T) {
	t.Parallel()

	loc, err := timezone(" Asia/Tokyo ")
	require.NoError(t, err)
	assert.Equal(t, "Asia/Tokyo", loc.String())

	loc, err = timezone("")
	require.NoError(t, err)
	assert.Nil(t, loc)

	_, err = timezone("Nowhere/Else")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location.timezone")
}
