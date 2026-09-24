package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Points within the tolerance of the line between their neighbours go, and a
// corner stays.
func TestSimplifyKeepsTheShape(t *testing.T) {
	t.Parallel()

	line := [][2]float64{{0, 0}, {1, 0.1}, {2, 0}, {3, 0.05}, {4, 0}}
	assert.Equal(t, [][2]float64{{0, 0}, {4, 0}}, simplify(line, 0.2))

	corner := [][2]float64{{0, 0}, {2, 0}, {2, 2}}
	assert.Equal(t, corner, simplify(corner, 0.2))
}

// Specks of islands go unless they are all a country has.
func TestKeptRingsDropsSpecksButNotWholeCountries(t *testing.T) {
	t.Parallel()

	speck := [][2]float64{{0, 0}, {0.01, 0}, {0.01, 0.01}, {0, 0}}
	land := [][2]float64{{0, 0}, {20, 0}, {20, 20}, {0, 0}}

	assert.Len(t, keptRings([][][][2]float64{{land}, {speck}}), 1)
	assert.Equal(t, [][][2]float64{speck}, keptRings([][][][2]float64{{speck}}))
}

// A territory filed under a country's code lends its outline, and the name and
// marker stay the country's whichever comes last.
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
