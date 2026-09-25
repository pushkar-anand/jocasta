package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShortenDropsLegalSuffixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, want string
	}{
		{"Amazon.com, Inc.", "Amazon"},
		{"Google LLC", "Google"},
		{"Cloudflare, Inc.", "Cloudflare"},
		{"Microsoft Corporation", "Microsoft"},
		{"Example Holdings, Inc.", "Example"},
		{"Example Networks", "Example Networks"},
		// Nothing but suffixes: keep the whole name.
		{"Inc.", "Inc."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, shorten(tt.name))
		})
	}
}
