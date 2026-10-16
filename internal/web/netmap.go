package web

import (
	"context"
	"fmt"
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
	// mapSpan is how far back the lines of the map go. The tables hold hours.
	// So it covers the hour before and the current hour.
	mapSpan = time.Hour

	// mapRecent is how far back a line counts as active: the whole window of
	// the recorder.
	mapRecent = 15 * time.Minute
)

// RecentTraffic is what the traffic recorder saw recently, by address. The map
// uses it to mark what is active. The recorder runs only when a traffic
// source is configured.
type RecentTraffic interface {
	Recent(since time.Time) []inventory.RecentEdge
}

// Option configures a Handler.
type Option func(*Handler)

// WithRecentTraffic makes the map mark what recent reports as active.
func WithRecentTraffic(recent RecentTraffic) Option {
	return func(h *Handler) { h.recent = recent }
}

// WithHomeCountry makes the world map draw its lines from a country, by its
// two-letter code. Use it when the outside address of the router cannot give
// the country of the network.
func WithHomeCountry(code string) Option {
	return func(h *Handler) { h.homeCountry = code }
}

// mapPage is the network map.
type mapPage struct {
	view

	// Recorded tells whether Jocasta recorded any traffic.
	Recorded bool

	// Watching tells whether the page can know what is active now. This is
	// true when the recorder runs in this process.
	Watching bool

	// World is set on the world view. Map and Layout are set on the network
	// view.
	World  *worldView
	Map    *inventory.TrafficMap
	Layout *netmap.Layout
}

// The two views of the map: the network as a tree, and the internet on the
// world.
const (
	mapViewNetwork = "network"
	mapViewWorld   = "world"
)

// mapView is the view that a request asks for. It is the network when the
// request names none.
func mapView(r *http.Request) string {
	if r.URL.Query().Get("view") == mapViewWorld {
		return mapViewWorld
	}

	return mapViewNetwork
}

// worldShades is the number of shade steps for countries on the world map.
const worldShades = 5

// worldView is the internet placed on the world.
type worldView struct {
	Width, Height float64

	// Outline is the shape of every country. Countries are the countries with
	// traffic.
	Outline   []geo.Country
	Countries []*worldCountry

	// Home is the country of the network, from which the lines start. It is
	// nil when it is not known.
	Home *geo.Country

	Attribution string
}

// worldCountry is the traffic of one country and its shade, with the line to
// it from home. The line is empty when home is not known, or is this
// country.
type worldCountry struct {
	*inventory.CountryTraffic

	Shade int

	Arc      string
	ArcWidth float64
}

// HomeKey is the name of home for selection, the other end of every arc.
func (w *worldView) HomeKey() string {
	if w.Home == nil {
		return ""
	}

	return countryKey(w.Home.Code)
}

// Key is the name of the country for selection, as for a device.
func (c *worldCountry) Key() string { return countryKey(c.Code) }

func countryKey(code string) string { return "c" + code }

// Shades are the steps of the legend of the world map.
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

// networkMapLive serves only the map, for the poll of the page. The wrapper
// that drives the poll stays on the page across every refresh.
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

// buildMap prepares the network or world map of the last hour. Before Jocasta
// records traffic, it returns page data without a layout, for the setup
// message.
func (h *Handler) buildMap(ctx context.Context, which string) (*mapPage, error) {
	now := h.store.Now()
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

// buildWorld puts the internet traffic of the last hour on the world. It shades
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

	if w.Home, err = h.homeOf(ctx, now); err != nil {
		return nil, err
	}

	var top int64 = 1
	for _, c := range countries {
		top = max(top, c.Bytes)
	}

	for _, c := range countries {
		wc := &worldCountry{CountryTraffic: c, Shade: shade(c.Bytes, top)}

		if to, ok := geo.CountryOf(c.Code); ok && w.Home != nil && to.Code != w.Home.Code {
			wc.Arc = arc(w.Home.LabelX, w.Home.LabelY, to.LabelX, to.LabelY)
			wc.ArcWidth = arcWidth(c.Bytes, top)
		}

		w.Countries = append(w.Countries, wc)
	}

	return w, nil
}

// arcBow is how far an arc bends up from the straight line, as a part of its
// length. It is enough to look like a route.
const arcBow = 0.25

// arc is a curve from one point on the world map to another, bent up. So the
// lines from one country spread apart.
func arc(x1, y1, x2, y2 float64) string {
	dx, dy := x2-x1, y2-y1
	length := math.Hypot(dx, dy)

	// The normal to the line, turned to point up the map.
	nx, ny := -dy/length, dx/length
	if ny > 0 {
		nx, ny = -nx, -ny
	}

	cx := (x1+x2)/2 + nx*length*arcBow
	cy := (y1+y2)/2 + ny*length*arcBow

	return fmt.Sprintf("M %.1f %.1f Q %.1f %.1f %.1f %.1f", x1, y1, cx, cy, x2, y2)
}

// arcWidth is the width of an arc for n bytes against the top of the busiest
// country, on a log scale.
func arcWidth(n, top int64) float64 {
	const thinnest, thickest = 0.6, 3.5

	if n <= 1 || top <= 1 {
		return thinnest
	}

	f := math.Log(float64(n)) / math.Log(float64(top))

	return math.Round((thinnest+(thickest-thinnest)*min(max(f, 0), 1))*10) / 10
}

// homeOf is the country of the network. It is the configured country. Else, it
// is the country of the outside address of the router. A private outside
// address, as behind the carrier-grade NAT of an ISP, gives nothing. If
// nothing gives a country, it returns nil.
func (h *Handler) homeOf(ctx context.Context, now time.Time) (*geo.Country, error) {
	if c, ok := geo.CountryOf(h.homeCountry); ok {
		return &c, nil
	}

	outside, err := h.store.OutsideAddresses(ctx, now.Add(-outsideAddrWindow))
	if err != nil {
		return nil, err
	}

	for _, a := range outside {
		code, ok := geo.Lookup(a)
		if !ok {
			continue
		}

		if c, ok := geo.CountryOf(code); ok {
			return &c, nil
		}
	}

	return nil, nil
}

// shade is the step, 1 to worldShades, of n bytes against the top of the
// busiest country, on a log scale.
func shade(n, top int64) int {
	if n <= 1 || top <= 1 {
		return 1
	}

	f := math.Log(float64(n)) / math.Log(float64(top))

	return min(max(int(math.Ceil(f*worldShades)), 1), worldShades)
}

// mapServices is the services of a link as a person reads them, "https · port
// 123 UDP". Each is its usual name or its port, with the protocol when that is
// not TCP.
func mapServices(list []inventory.MapService) string {
	names := make([]string, 0, len(list))

	for _, s := range list {
		names = append(names, serviceName(s.Protocol, s.Port, s.Name))
	}

	return strings.Join(names, " · ")
}

// networkLabel is the name of a network, with its VLAN when it has one. It is
// its prefix when it has no name.
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
