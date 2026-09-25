package main

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/notify"
)

func TestNotifyDestinationsBuildsOnlyEnabledOnesInNameOrder(t *testing.T) {
	t.Parallel()

	off := false

	var cfg config.Config

	cfg.Notify = map[string]notify.Config{
		"phone":      {Ntfy: &notify.Ntfy{URL: "https://ntfy.example.com/jocasta"}},
		"tablet":     {Enabled: &off, Ntfy: &notify.Ntfy{URL: "https://ntfy.example.com/jocasta"}},
		"automation": {Webhook: &notify.Webhook{URL: "https://hooks.example.com/jocasta", Secret: "s"}},
	}

	got, err := notifyDestinations(t.Context(), &cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "automation", got[0].Name())
	assert.Equal(t, "phone", got[1].Name())
}

func TestNotifyDestinationsRefusesABadEntry(t *testing.T) {
	t.Parallel()

	var cfg config.Config

	cfg.Notify = map[string]notify.Config{
		"automation": {Webhook: &notify.Webhook{URL: "https://hooks.example.com/jocasta"}},
	}

	_, err := notifyDestinations(t.Context(), &cfg, slog.New(slog.DiscardHandler))
	require.Error(t, err, "a webhook needs a secret")
	assert.Contains(t, err.Error(), `"automation"`)
}
