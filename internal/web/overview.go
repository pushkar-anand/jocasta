package web

import (
	"context"
	"fmt"
	"net/http"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// overviewData is the whole overview, and also every part of it that refreshes
// on its own, since the fragment is rendered from the same value.
type overviewData struct {
	view
	Stats    *inventory.Stats
	Scan     *inventory.Scan
	Networks []*inventory.Network
	Events   []*inventory.Event
}

func (h *Handler) overview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := buildOverviewData(r.Context(), h.store)
		if err != nil {
			h.fail(w, r, err)

			return
		}

		h.htmlWriter.HTML(templatePageDashboard, data)
	}
}

// overviewLive serves the part of the overview that goes stale, which is what
// the page polls for. It answers with the body alone: the #live wrapper that
// drives the poll stays on the page across every refresh.
func (h *Handler) overviewLive() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := buildOverviewData(r.Context(), h.store)
		if err != nil {
			h.fail(w, r, err)

			return
		}

		h.htmlWriter.HTML("partial/live-body", data)
	}
}

func buildOverviewData(
	ctx context.Context,
	store *inventory.Store,
) (*overviewData, error) {
	stats, err := store.Stats(ctx)
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}

	networks, err := store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	// The overview shows the top of the log and never walks it, so it takes
	// the first page and drops the cursor that would continue it.
	activity, err := store.ListEvents(ctx, inventory.Page{Limit: activityLimit})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	data := &overviewData{
		view:     view{Title: "Overview", Section: "Overview", Live: true},
		Stats:    stats,
		Networks: networks,
		Events:   activity.Events,
	}

	// A first run has no sweep behind it, which is a state to render rather
	// than a failure to report.
	if scan, err := store.LatestScan(ctx); err == nil {
		data.Scan = scan
		data.Note = sweepNote(scan)
	}

	return data, nil
}
