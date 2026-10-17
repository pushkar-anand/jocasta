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

// WorldWidth and WorldHeight are the dimensions of the canvas of the world
// map. Its equirectangular projection includes Greenland and Svalbard, but not
// Antarctica.
const (
	WorldWidth  = 1000.0
	WorldHeight = WorldWidth * (worldNorth - worldSouth) / 360

	worldNorth = 84.0
	worldSouth = -58.0
)

// Project puts a longitude and latitude, in degrees, on the canvas of the
// world map. It clamps the latitude to the north and south edges of the map.
func Project(lon, lat float64) (x, y float64) {
	lat = math.Max(worldSouth, math.Min(worldNorth, lat))

	return (lon + 180) / 360 * WorldWidth, (worldNorth - lat) / (worldNorth - worldSouth) * WorldHeight
}

// Country is the outline of one country on the world map.
type Country struct {
	// Code is the two-letter ISO 3166 code that Lookup returns.
	Code string
	Name string

	// Path is the outline as SVG path data on the canvas of the world map.
	// LabelX and LabelY are the position of the marker, in the country.
	Path           string
	LabelX, LabelY float64
}

// worldData holds outlines from the 1:10m countries of Natural Earth, which are
// in the public domain. internal/world simplifies them into world.gz.
//
//go:embed world.gz
var worldData []byte

var world = sync.OnceValue(loadWorld)

type worldIndex struct {
	list   []Country
	byCode map[string]int
}

// loadWorld indexes the embedded outlines by country code. If it cannot read
// the gzip header, it returns an empty index. It skips malformed rows.
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

// World returns the outline of every country, by code. The slice and its
// elements are shared. Do not change them.
func World() []Country { return world().list }

// CountryOf returns the country with a two-letter code. It returns false for a
// country that the map does not draw.
func CountryOf(code string) (Country, bool) {
	ix := world()

	i, ok := ix.byCode[code]
	if !ok {
		return Country{}, false
	}

	return ix.list[i], true
}
