// Command world builds the world outline embedded by package geo from Natural
// Earth's countries, which are in the public domain.
//
// Natural Earth publishes the edition used here only at 1:10m, so the outline
// is simplified to what a map the width of a screen can show.
//
// Borders move rarely, so unlike the country table this is run by hand, not
// on a schedule. Each country becomes one line, "code<TAB>name<TAB>x<TAB>y
// <TAB>path": its ISO code, its name, where its marker goes, and its outline
// as SVG path data, already projected, so the map draws it as is.
package main

import (
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/pkg/geo"
)

const sourceURL = "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/master/geojson/ne_10m_admin_0_countries_ind.geojson"

const userAgent = "jocasta-world-generator/1 (+https://github.com/pushkar-anand/jocasta)"

// minCountries guards against a truncated or changed source.
const minCountries = 150

// tolerance is how far, in canvas units, simplifying may move an outline:
// detail finer than the map can show. minRing drops an island whose outline
// is smaller than this across, unless it is all a country has.
const (
	tolerance = 0.35
	minRing   = 1.2
)

// folded are the territories Natural Earth draws on their own, with no code,
// that address registries file under a country: their outline joins it.
var folded = map[string]string{
	"N. Cyprus":  "CY",
	"Somaliland": "SO",
}

// skipped are drawn by Natural Earth but left off the map.
var skipped = map[string]bool{"AQ": true}

type feature struct {
	Properties struct {
		Name   string  `json:"NAME"`
		ISO    string  `json:"ISO_A2_EH"`
		LabelX float64 `json:"LABEL_X"`
		LabelY float64 `json:"LABEL_Y"`
	} `json:"properties"`
	Geometry struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
}

type country struct {
	code, name string
	x, y       float64
	path       strings.Builder

	// area is the size of the feature the name and marker came from.
	area float64
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	var src struct {
		Features []feature `json:"features"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&src); err != nil {
		return err
	}

	countries, err := build(src.Features)
	if err != nil {
		return err
	}

	if len(countries) < minCountries {
		return fmt.Errorf("got %d countries, expected at least %d", len(countries), minCountries)
	}

	return write(countries)
}

// build projects every feature and gathers them by country.
func build(features []feature) ([]*country, error) {
	byCode := make(map[string]*country)

	for _, f := range features {
		p := f.Properties
		code, isFold := folded[p.Name]

		if !isFold {
			code = p.ISO
		}

		if len(code) != 2 || skipped[code] {
			continue
		}

		c, ok := byCode[code]
		if !ok {
			c = &country{code: code}
			byCode[code] = c
		}

		polygons, err := polygonsOf(f.Geometry.Type, f.Geometry.Coordinates)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}

		// A folded territory lends its outline, not its name or marker. So
		// does any smaller feature sharing a country's code: Natural Earth
		// files territories such as Ashmore and Cartier Is. under AU, and the
		// country is the biggest of them whatever order they come in.
		if a := area(polygons); !isFold && a > c.area {
			c.name = p.Name
			c.x, c.y = geo.Project(p.LabelX, p.LabelY)
			c.area = a
		}

		for _, ring := range keptRings(polygons) {
			writeRing(&c.path, ring)
		}
	}

	out := make([]*country, 0, len(byCode))

	for _, c := range byCode {
		if c.name != "" && c.path.Len() > 0 {
			out = append(out, c)
		}
	}

	slices.SortFunc(out, func(a, b *country) int { return cmp.Compare(a.code, b.code) })

	return out, nil
}

// polygonsOf reads a Polygon or MultiPolygon's coordinates as polygons of
// rings of [longitude, latitude] points.
func polygonsOf(kind string, raw json.RawMessage) ([][][][2]float64, error) {
	switch kind {
	case "Polygon":
		var p [][][2]float64
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}

		return [][][][2]float64{p}, nil
	case "MultiPolygon":
		var mp [][][][2]float64
		if err := json.Unmarshal(raw, &mp); err != nil {
			return nil, err
		}

		return mp, nil
	default:
		return nil, fmt.Errorf("unexpected geometry %s", kind)
	}
}

// area is the size of the polygons' outer rings in square degrees, which is
// enough to tell a country from an island filed under its code.
func area(polygons [][][][2]float64) float64 {
	var total float64

	for _, poly := range polygons {
		if len(poly) == 0 {
			continue
		}

		ring := poly[0]

		var twice float64
		for i := range len(ring) - 1 {
			twice += ring[i][0]*ring[i+1][1] - ring[i+1][0]*ring[i][1]
		}

		total += math.Abs(twice) / 2
	}

	return total
}

// keptRings is every ring of the polygons big enough to see, or the biggest
// alone when none is, so a small island nation still has a shape.
func keptRings(polygons [][][][2]float64) [][][2]float64 {
	var (
		kept    [][][2]float64
		biggest [][2]float64
		most    float64
	)

	for _, poly := range polygons {
		for _, ring := range poly {
			span := extent(ring)
			if span >= minRing {
				kept = append(kept, ring)
			}

			if span > most {
				biggest, most = ring, span
			}
		}
	}

	if len(kept) == 0 && biggest != nil {
		kept = append(kept, biggest)
	}

	return kept
}

// extent is how far a ring reaches across the canvas, its wider side.
func extent(ring [][2]float64) float64 {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)

	for _, pt := range ring {
		x, y := geo.Project(pt[0], pt[1])
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}

	return math.Max(maxX-minX, maxY-minY)
}

// writeRing appends one closed ring as projected path data, simplified so no
// point it drops moved the outline by more than tolerance.
func writeRing(b *strings.Builder, ring [][2]float64) {
	pts := make([][2]float64, len(ring))
	for i, pt := range ring {
		x, y := geo.Project(pt[0], pt[1])
		pts[i] = [2]float64{x, y}
	}

	// A small island simplifies to nothing; it is tried again finer, so a
	// country whose only land it is still has a shape.
	var kept [][2]float64
	for tol := tolerance; len(kept) < 4 && tol > 0.01; tol /= 4 {
		kept = simplify(pts, tol)
	}

	if len(kept) < 3 {
		return
	}

	pts = kept

	for i, pt := range pts {
		if i == 0 {
			b.WriteString("M")
		} else {
			b.WriteString("L")
		}

		b.WriteString(num(pt[0]) + " " + num(pt[1]))
	}

	b.WriteString("Z")
}

// simplify is Douglas-Peucker: keep the ends, and the point furthest from the
// line between them if it is further than tol, then the same on each side.
func simplify(pts [][2]float64, tol float64) [][2]float64 {
	if len(pts) < 3 {
		return pts
	}

	far, at := 0.0, 0

	for i := 1; i < len(pts)-1; i++ {
		if d := offLine(pts[i], pts[0], pts[len(pts)-1]); d > far {
			far, at = d, i
		}
	}

	if far <= tol {
		return [][2]float64{pts[0], pts[len(pts)-1]}
	}

	left := simplify(pts[:at+1], tol)
	right := simplify(pts[at:], tol)

	return append(left[:len(left)-1], right...)
}

// offLine is how far p is from the segment a-b.
func offLine(p, a, b [2]float64) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]

	l := dx*dx + dy*dy
	if l == 0 {
		return math.Hypot(p[0]-a[0], p[1]-a[1])
	}

	t := math.Max(0, math.Min(1, ((p[0]-a[0])*dx+(p[1]-a[1])*dy)/l))

	return math.Hypot(p[0]-(a[0]+t*dx), p[1]-(a[1]+t*dy))
}

func num(f float64) string { return strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64) }

func write(countries []*country) error {
	f, err := os.Create("world.gz")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(zw)

	for _, c := range countries {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.code, c.name, num(c.x), num(c.y), c.path.String()); err != nil {
			return err
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}

	if err := zw.Close(); err != nil {
		return err
	}

	log.Printf("wrote world.gz: %d countries", len(countries))

	return f.Close()
}
