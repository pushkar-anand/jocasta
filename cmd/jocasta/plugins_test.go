package main

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/config"
)

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
// anyone, so it stops startup instead.
func TestTrafficReportersRefusesAnInstanceWithoutExporters(t *testing.T) {
	t.Parallel()

	var cfg config.Config

	cfg.Plugins.NetFlow = map[string]config.NetFlow{"gateway": {Enabled: true}}

	_, err := trafficReporters(t.Context(), &cfg, slog.New(slog.DiscardHandler))
	require.Error(t, err)
}

// A home country is taken in any case and checked against the map, so a typo
// fails startup rather than quietly drawing no lines.
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
	assert.Contains(t, err.Error(), "traffic.home_country")
}
