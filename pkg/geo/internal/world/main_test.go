package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The simplification removes points within the tolerance of the line between
// their neighbours. A corner stays.
func TestSimplifyKeepsTheShape(t *testing.T) {
	t.Parallel()

	line := [][2]float64{{0, 0}, {1, 0.1}, {2, 0}, {3, 0.05}, {4, 0}}
	assert.Equal(t, [][2]float64{{0, 0}, {4, 0}}, simplify(line, 0.2))

	corner := [][2]float64{{0, 0}, {2, 0}, {2, 2}}
	assert.Equal(t, corner, simplify(corner, 0.2))
}

// The build removes very small islands, unless they are the only land of a
// country.
func TestKeptRingsDropsSpecksButNotWholeCountries(t *testing.T) {
	t.Parallel()

	speck := [][2]float64{{0, 0}, {0.01, 0}, {0.01, 0.01}, {0, 0}}
	land := [][2]float64{{0, 0}, {20, 0}, {20, 20}, {0, 0}}

	assert.Len(t, keptRings([][][][2]float64{{land}, {speck}}), 1)
	assert.Equal(t, [][][2]float64{speck}, keptRings([][][][2]float64{{speck}}))
}

// A territory under the code of a country gives its outline. The name and
// marker stay with the country, in any order.
func TestBuildNamesACountryAfterItsBiggestFeature(t *testing.T) {
	t.Parallel()

	square := func(name string, lon, lat, side float64) feature {
		var f feature

		f.Properties.Name = name
		f.Properties.ISO = "AU"
		f.Properties.LabelX, f.Properties.LabelY = lon+side/2, lat+side/2
		f.Geometry.Type = "Polygon"
		f.Geometry.Coordinates, _ = json.Marshal([][][2]float64{{
			{lon, lat}, {lon + side, lat}, {lon + side, lat + side}, {lon, lat + side}, {lon, lat},
		}})

		return f
	}

	mainland := square("Australia", 115, -38, 30)
	islands := square("Ashmore and Cartier Is.", 123, -12.5, 0.2)

	for _, order := range [][]feature{{mainland, islands}, {islands, mainland}} {
		got, err := build(order)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "Australia", got[0].name)
		assert.Contains(t, got[0].path.String(), "Z")
	}
}
