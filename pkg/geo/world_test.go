package geo

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectMapsTheCorners(t *testing.T) {
	t.Parallel()

	x, y := Project(-180, worldNorth)
	assert.InDelta(t, 0, x, 1e-9)
	assert.InDelta(t, 0, y, 1e-9)

	x, y = Project(180, worldSouth)
	assert.InDelta(t, WorldWidth, x, 1e-9)
	assert.InDelta(t, WorldHeight, y, 1e-9)

	_, y = Project(0, -90)
	assert.InDelta(t, WorldHeight, y, 1e-9, "the far south is held to the bottom edge")
}

// Every country the map draws has a name, an outline and a marker on the
// canvas, and the ones traffic most often goes to are there.
func TestWorldDrawsEveryCountry(t *testing.T) {
	t.Parallel()

	countries := World()
	require.Greater(t, len(countries), 150)

	for _, c := range countries {
		assert.Len(t, c.Code, 2)
		assert.NotEmpty(t, c.Name, c.Code)
		assert.True(t, strings.HasPrefix(c.Path, "M") && strings.HasSuffix(c.Path, "Z"), c.Code)
		assert.True(t, c.LabelX >= 0 && c.LabelX <= WorldWidth, c.Code)
		assert.True(t, c.LabelY >= 0 && c.LabelY <= WorldHeight, c.Code)
	}

	for _, code := range []string{"IN", "US", "DE", "SG", "JP", "IE", "NL", "AU"} {
		_, ok := CountryOf(code)
		assert.True(t, ok, code)
	}

	_, ok := CountryOf("AQ")
	assert.False(t, ok, "Antarctica is left off")
}

// A country sharing its code with small territories is named and marked as the
// country, not as whichever territory the source listed last.
func TestCountryOfNamesTheCountryNotATerritory(t *testing.T) {
	t.Parallel()

	for code, name := range map[string]string{"AU": "Australia", "BR": "Brazil", "FR": "France"} {
		c, ok := CountryOf(code)
		require.True(t, ok, code)
		assert.Equal(t, name, c.Name)
	}
}
