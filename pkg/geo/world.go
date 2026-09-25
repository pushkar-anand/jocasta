package geo

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"math"
	"strconv"
	"strings"
	"sync"
)

// The world map is drawn in an equirectangular projection on a canvas
// WorldWidth across, from latitude worldNorth down to worldSouth: far enough
// north for Greenland and Svalbard, and short of Antarctica, which nobody's
// traffic goes to.
const (
	WorldWidth  = 1000.0
	WorldHeight = WorldWidth * (worldNorth - worldSouth) / 360

	worldNorth = 84.0
	worldSouth = -58.0
)

// Project places a longitude and latitude on the world map's canvas.
func Project(lon, lat float64) (x, y float64) {
	lat = math.Max(worldSouth, math.Min(worldNorth, lat))

	return (lon + 180) / 360 * WorldWidth, (worldNorth - lat) / (worldNorth - worldSouth) * WorldHeight
}

// Country is one country's outline on the world map.
type Country struct {
	// Code is the two-letter ISO 3166 code Lookup returns.
	Code string
	Name string

	// Path is the outline as SVG path data on the world map's canvas, and
	// LabelX, LabelY where a marker for the country belongs, inside it.
	Path           string
	LabelX, LabelY float64
}

// The outlines are from Natural Earth's 1:10m countries, which are in the
// public domain, simplified and generated into world.gz by internal/world.
//
//go:embed world.gz
var worldData []byte

var world = sync.OnceValue(loadWorld)

type worldIndex struct {
	list   []Country
	byCode map[string]int
}

func loadWorld() *worldIndex {
	ix := &worldIndex{byCode: make(map[string]int)}

	zr, err := gzip.NewReader(bytes.NewReader(worldData))
	if err != nil {
		return ix
	}

	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\t", 5)
		if len(f) != 5 {
			continue
		}

		x, errX := strconv.ParseFloat(f[2], 64)
		y, errY := strconv.ParseFloat(f[3], 64)

		if errX != nil || errY != nil {
			continue
		}

		ix.byCode[f[0]] = len(ix.list)
		ix.list = append(ix.list, Country{Code: f[0], Name: f[1], LabelX: x, LabelY: y, Path: f[4]})
	}

	return ix
}

// World returns every country's outline, by code.
func World() []Country { return world().list }

// CountryOf returns the country with a two-letter code, and false for one the
// map does not draw.
func CountryOf(code string) (Country, bool) {
	ix := world()

	i, ok := ix.byCode[code]
	if !ok {
		return Country{}, false
	}

	return ix.list[i], true
}
