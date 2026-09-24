package web

import (
	"cmp"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/web/netmap"
)

const (
	// mapSpan is how far back the map's lines reach. The tables hold hours,
	// so it covers the hour before and the one in progress.
	mapSpan = time.Hour

	// mapRecent is how far back a line counts as active: the recorder's
	// whole window.
	mapRecent = 15 * time.Minute
)

// RecentTraffic is what the traffic recorder saw lately, by address, for the
// map to mark what is active. The recorder only runs when a traffic source is
// configured.
type RecentTraffic interface {
	Recent(since time.Time) []inventory.RecentEdge
}

// Option configures a Handler.
type Option func(*Handler)

// WithRecentTraffic has the map mark what recent reports as active.
func WithRecentTraffic(recent RecentTraffic) Option {
	return func(h *Handler) { h.recent = recent }
}

// mapPage is the network map.
type mapPage struct {
	view

	// Recorded is whether any traffic has been recorded at all.
	Recorded bool

	// Watching is whether what is active now can be told apart: the
	// recorder runs in this process.
	Watching bool

	Map    *inventory.TrafficMap
	Layout *netmap.Layout
}

// networkMap serves the map page.
func (h *Handler) networkMap(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		data, err := h.buildMap(ctx)
		if err != nil {
			return err
		}

		data.view = view{
			Title: "Map", Section: "Map", Live: liveEvery(data.Recorded, "minute"),
			Role: sm.CurrentRole(ctx), SignedInAs: sm.CurrentUsername(ctx),
		}

		h.htmlWriter.Success(w, r, templatePageMap, data)

		return nil
	}
}

// networkMapLive serves the map alone, which is what the page polls for. The
// wrapper that drives the poll stays on the page across every refresh.
func (h *Handler) networkMapLive() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		data, err := h.buildMap(r.Context())
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialMapBody, data)

		return nil
	}
}

func (h *Handler) buildMap(ctx context.Context) (*mapPage, error) {
	now := time.Now()
	data := &mapPage{Watching: h.recent != nil}

	var err error

	if data.Recorded, err = h.store.TrafficRecorded(ctx); err != nil {
		return nil, err
	}

	if !data.Recorded {
		return data, nil
	}

	var recent []inventory.RecentEdge
	if h.recent != nil {
		recent = h.recent.Recent(now.Add(-mapRecent))
	}

	if data.Map, err = h.store.TrafficMap(ctx, now.Add(-mapSpan), recent); err != nil {
		return nil, err
	}

	nets, err := h.store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	segments := make([]netmap.Segment, 0, len(nets)+1)
	for _, n := range nets {
		segments = append(segments, netmap.Segment{ID: n.ID, Label: networkLabel(n)})
	}

	elsewhere := "Elsewhere on your network"
	if len(nets) == 0 {
		elsewhere = "Your network"
	}

	segments = append(segments, netmap.Segment{Label: elsewhere})

	data.Layout = netmap.Place(data.Map, segments)

	return data, nil
}

// mapServices is a link's services as a person reads them, "https · port 123
// UDP": each one's usual name or its port, with the protocol when that is
// not TCP.
func mapServices(list []inventory.MapService) string {
	names := make([]string, 0, len(list))

	for _, s := range list {
		label := s.Name

		switch {
		case s.Port == 0:
			label = cmp.Or(protoName(s.Protocol), "TCP")
		case label == "":
			label = "port " + strconv.Itoa(int(s.Port))
		}

		if proto := protoName(s.Protocol); proto != "" && s.Port != 0 {
			label += " " + proto
		}

		names = append(names, label)
	}

	return strings.Join(names, " · ")
}

// networkLabel is a network's name, with its VLAN when it has one, or its
// prefix when it has no name.
func networkLabel(n *inventory.Network) string {
	label := n.Name
	if label == "" {
		label = n.CIDR
	}

	if n.VLAN != 0 {
		label += " · VLAN\u00a0" + strconv.Itoa(n.VLAN)
	}

	return label
}
