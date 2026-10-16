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
		{"Example Networks Pvt. Ltd.,", "Example Networks"},
		{"Example Pte Ltd", "Example"},
		{"Example Telephone Co INC", "Example Telephone"},
		{"Example Sdn Bhd", "Example"},
		// The name has only suffixes, so keep the whole name.
		{"Inc.", "Inc."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, shorten(tt.name))
		})
	}
}
