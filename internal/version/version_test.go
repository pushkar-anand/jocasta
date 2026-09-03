package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFallsBackWhenNothingLinkedIn(t *testing.T) {
	t.Parallel()

	// A test binary carries no linked-in values and no VCS stamps, so the
	// version resolves to the placeholder rather than an empty string.
	got := Get()

	assert.Equal(t, "dev", got.Version)
}

func TestInfoString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		info Info
		want []string
	}{
		"version only": {
			info: Info{Version: "1.4.0"},
			want: []string{"jocasta 1.4.0", "go "},
		},
		"full": {
			info: Info{
				Version: "1.4.0",
				Commit:  "0123456789abcdef0123456789abcdef01234567",
				Date:    "2026-09-03T09:25:14Z",
			},
			want: []string{"jocasta 1.4.0", "commit  0123456789ab", "built   2026-09-03T09:25:14Z"},
		},
		"modified": {
			info: Info{Version: "1.4.0", Modified: true},
			want: []string{"jocasta 1.4.0 (modified)"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tt.info.String()
			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestShortCommit(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "0123456789ab", shortCommit("0123456789abcdef"))
	assert.Equal(t, "abc123", shortCommit("abc123"))
}
