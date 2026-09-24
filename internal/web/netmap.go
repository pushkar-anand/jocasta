package web

import (
	"cmp"
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/web/netmap"
	"github.com/pushkar-anand/jocasta/pkg/geo"
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

// WithHomeCountry has the world map draw its lines from a country, given by
// its two-letter code, when the router's outside address cannot place the
// network.
func WithHomeCountry(code string) Option {
	return func(h *Handler) { h.homeCountry = code }
}

// mapPage is the network map.
type mapPage struct {
	view

	// Recorded is whether any traffic has been recorded at all.
	Recorded bool

	// Watching is whether what is active now can be told apart: the
	// recorder runs in this process.
	Watching bool

	// World is set on the world view, and Map and Layout on the network
	// one.
	World  *worldView
	Map    *inventory.TrafficMap
	Layout *netmap.Layout
}

// The map's two views: the network as a tree, and the internet on the world.
const (
	mapViewNetwork = "network"
	mapViewWorld   = "world"
)

// mapView is the view a request asks for, the network when it names none.
func mapView(r *http.Request) string {
	if r.URL.Query().Get("view") == mapViewWorld {
		return mapViewWorld
	}

	return mapViewNetwork
}

// worldShades is how many steps the world map shades countries in.
const worldShades = 5

// worldView is the internet placed on the world.
type worldView struct {
	Width, Height float64

	// Outline is every country's shape; Countries the ones with traffic.
	Outline   []geo.Country
	Countries []*worldCountry

	// Home is the country the network is in, which the lines are drawn
	// from; nil when it is not known.
	Home *geo.Country

	Attribution string
}

// worldCountry is one country's traffic and how dark it is shaded.
type worldCountry struct {
	*inventory.CountryTraffic

	Shade int
}

// Key is the country's name for selection, as a device's is.
func (c *worldCountry) Key() string { return countryKey(c.Code) }

func countryKey(code string) string { return "c" + code }

// Shades are the steps of the world map's legend.
func (w *worldView) Shades() []int {
	out := make([]int, worldShades)
	for i := range out {
		out[i] = i + 1
	}

	return out
}

// networkMap serves the map page.
func (h *Handler) networkMap(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		data, err := h.buildMap(ctx, mapView(r))
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
		data, err := h.buildMap(r.Context(), mapView(r))
		if err != nil {
			return err
		}

		if data.World != nil {
			h.htmlWriter.Success(w, r, templatePartialMapWorldBody, data)

			return nil
		}

		h.htmlWriter.Success(w, r, templatePartialMapBody, data)

		return nil
	}
}

func (h *Handler) buildMap(ctx context.Context, which string) (*mapPage, error) {
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

	if which == mapViewWorld {
		data.World, err = h.buildWorld(ctx, now, recent)

		return data, err
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

// buildWorld places the last hour's internet traffic on the world, shading
// each country on a log scale against the busiest.
func (h *Handler) buildWorld(ctx context.Context, now time.Time, recent []inventory.RecentEdge) (*worldView, error) {
	countries, err := h.store.TrafficByCountry(ctx, now.Add(-mapSpan), recent)
	if err != nil {
		return nil, err
	}

	w := &worldView{
		Width: geo.WorldWidth, Height: geo.WorldHeight,
		Outline: geo.World(), Attribution: geo.Attribution,
	}

	if home, ok := geo.CountryOf(h.homeCountry); ok {
		w.Home = &home
	}

	var top int64 = 1
	for _, c := range countries {
		top = max(top, c.Bytes)
	}

	for _, c := range countries {
		w.Countries = append(w.Countries, &worldCountry{CountryTraffic: c, Shade: shade(c.Bytes, top)})
	}

	return w, nil
}

// shade is the step, 1 to worldShades, n bytes falls in against the busiest
// country's top, on a log scale.
func shade(n, top int64) int {
	if n <= 1 || top <= 1 {
		return 1
	}

	f := math.Log(float64(n)) / math.Log(float64(top))

	return min(max(int(math.Ceil(f*worldShades)), 1), worldShades)
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
