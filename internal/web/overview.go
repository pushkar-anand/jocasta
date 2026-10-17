package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

const (
	overviewServiceLimit   = 5
	overviewPortEventLimit = 2

	// collectionStaleFloor is the shortest gap after a clean sweep before the
	// overview calls the presence counts old. Below it, a missed run is more
	// likely a schedule delay than a stopped collector.
	collectionStaleFloor = 30 * time.Minute
)

// overviewData is the whole overview. It is also every part of it that
// refreshes by itself, because the fragment renders from the same value.
type overviewData struct {
	view
	Stats      *inventory.Stats
	Scan       *inventory.Scan
	Ports      *inventory.PortOverview
	PortScan   *inventory.Scan
	PortEvents []*inventory.Event
	Networks   []*inventory.Network
	Events     []*inventory.Event

	// LastCollected is when a device sweep last finished with a result. It is
	// zero before the first. Stale is set when the newest sweep did not finish
	// cleanly. It is also set when no sweep finished for so long that the
	// presence counts are probably behind the devices.
	LastCollected time.Time
	Stale         bool
}

// overview serves the inventory summary, with the controls of the signed-in
// account. A failed read returns an error. It does not show an empty
// inventory.
func (h *Handler) overview(sm *auth.Session) response.HandlerFunc {
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

		data.Role = sm.CurrentRole(r.Context())
		data.SignedInAs = sm.CurrentUsername(r.Context())

		h.htmlWriter.Success(w, r, templatePageDashboard, data)

		return nil
	}
}

// overviewLive serves the part of the overview that gets old. The page polls
// for it. It answers with only the body. The #live wrapper that drives the
// poll stays on the page across every refresh.
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

// buildOverviewData reads everything that the overview shows: the counts, the
// networks, the newest changes and port events, the latest sweep and port
// scan, and whether the presence counts look old.
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

	// The overview shows the top of the log, and never walks it. So it takes
	// the first page, and drops the cursor that would continue it.
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
		// Live drives the "refreshing" line of the topbar. The page mounts the
		// poller only when it has an inventory to poll for. An empty inventory
		// renders the invitation, with nothing that ticks.
		Title: "Overview", Section: "Overview", Live: liveEvery(stats.Total > 0, "30s"),
		Window:     windowWords(store.OnlineWindow()),
		Stats:      stats,
		Ports:      ports,
		PortEvents: portActivity.Events,
		Networks:   networks,
		Events:     activity.Events,
	}

	// A first run has no sweep yet. The page renders that state, and reports
	// no error.
	if scan, err := store.LatestScan(ctx); err == nil {
		data.Scan = scan
		data.Note = sweepNote(store.Now(), scan)
	}

	if scan, err := store.LatestScanOfKind(ctx, dbtype.ScanPorts); err == nil {
		data.PortScan = scan
	}

	data.LastCollected = lastSweptAt(ctx, store)
	data.Stale = staleCollection(ctx, store, data.LastCollected, stats.Total)

	return data, nil
}

// staleCollection reports whether the presence counts on the overview need
// care. This is when the newest sweep failed or stopped early. It is also when
// no clean sweep finished for so long that a stopped collector is the more
// likely reason why a device looks quiet.
func staleCollection(ctx context.Context, store *inventory.Store, lastCollected time.Time, devices int) bool {
	if latest, err := store.LatestScanOfKind(ctx, dbtype.ScanDiscovery); err == nil &&
		(latest.Status == dbtype.StatusFailed || latest.Status == dbtype.StatusCancelled) {
		return true
	}

	if devices == 0 {
		return false
	}

	behind := 2 * max(store.OnlineWindow(), collectionStaleFloor)

	return lastCollected.IsZero() || store.Now().Sub(lastCollected) > behind
}
