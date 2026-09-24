package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
