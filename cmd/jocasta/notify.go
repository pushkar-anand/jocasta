package main

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/notify"
)

// notifyDestinations builds every enabled place changes are sent, in name
// order. A destination is on unless it says enabled: false, and one that
// cannot be built is a config error, the same as a plugin that cannot.
func notifyDestinations(ctx context.Context, cfg *config.Config, log *slog.Logger) ([]*notify.Destination, error) {
	names := slices.Sorted(maps.Keys(cfg.Notify))
	out := make([]*notify.Destination, 0, len(names))

	for _, name := range names {
		c := cfg.Notify[name]
		if !c.On() {
			log.InfoContext(ctx, "destination is configured but not enabled", slog.String("destination", name))
			continue
		}

		d, err := notify.NewDestination(name, c)
		if err != nil {
			return nil, err
		}

		out = append(out, d)
	}

	return out, nil
}
