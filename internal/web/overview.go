package web

import (
	"context"
	"fmt"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

const (
	overviewServiceLimit   = 5
	overviewPortEventLimit = 2
)

// overviewData is the whole overview, and also every part of it that refreshes
// on its own, since the fragment is rendered from the same value.
type overviewData struct {
	view
	Stats      *inventory.Stats
	Scan       *inventory.Scan
	Ports      *inventory.PortOverview
	PortScan   *inventory.Scan
	PortEvents []*inventory.Event
	Networks   []*inventory.Network
	Events     []*inventory.Event
}

func (h *Handler) overview() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		data, err := buildOverviewData(r.Context(), h.store)
		if err != nil {
			h.log.ErrorContext(
				r.Context(),
				"failed to build overview data",
				logger.Err(err),
			)

			return err
		}

		h.htmlWriter.Success(w, r, templatePageDashboard, data)
		return nil
	}
}

// overviewLive serves the part of the overview that goes stale, which is what
// the page polls for. It answers with the body alone: the #live wrapper that
// drives the poll stays on the page across every refresh.
func (h *Handler) overviewLive() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		data, err := buildOverviewData(r.Context(), h.store)
		if err != nil {
			h.log.ErrorContext(r.Context(), "failed to build overview data",
				logger.Err(err))

			return err
		}

		h.htmlWriter.Success(w, r, templatePartialLiveOverview, data)
		return nil
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

	ports, err := store.PortOverview(ctx, overviewServiceLimit)
	if err != nil {
		return nil, err
	}

	portActivity, err := store.ListEvents(ctx, inventory.Page{
		Limit:          overviewPortEventLimit,
		EventKinds:     []dbtype.EventKind{dbtype.EventPortOpened, dbtype.EventPortClosed},
		ExcludeIgnored: true,
	})
	if err != nil {
		return nil, fmt.Errorf("list port events: %w", err)
	}

	data := &overviewData{
		Title: "Overview", Section: "Overview", Live: true,
		Stats:      stats,
		Ports:      ports,
		PortEvents: portActivity.Events,
		Networks:   networks,
		Events:     activity.Events,
	}

	// A first run has no sweep behind it, which is a state to render rather
	// than a failure to report.
	if scan, err := store.LatestScan(ctx); err == nil {
		data.Scan = scan
		data.Note = sweepNote(scan)
	}

	if scan, err := store.LatestScanOfKind(ctx, dbtype.ScanPorts); err == nil {
		data.PortScan = scan
	}

	return data, nil
}
