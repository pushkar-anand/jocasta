package inventory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// at renders a claim's sighting, later for a larger n.
func at(n int) dbtype.Time {
	return dbtype.NewTime(time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC))
}

func TestResolveHostname(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claims []nameClaim
		want   nameClaim
	}{
		{
			name: "no claims name the device",
		},
		{
			name:   "a claim with no name cannot win",
			claims: []nameClaim{{name: "", standing: dbtype.HostnameFromDNS, at: at(2)}},
		},
		{
			name: "reverse DNS outranks a static lease",
			claims: []nameClaim{
				{name: "leased", standing: dbtype.HostnameFromDHCPStatic, at: at(2)},
				{name: "resolved", standing: dbtype.HostnameFromDNS, at: at(1)},
			},
			want: nameClaim{name: "resolved", standing: dbtype.HostnameFromDNS, at: at(1)},
		},
		{
			name: "a static lease outranks a dynamic one",
			claims: []nameClaim{
				{name: "dynamic", standing: dbtype.HostnameFromDHCPLease, at: at(2)},
				{name: "bound", standing: dbtype.HostnameFromDHCPStatic, at: at(1)},
			},
			want: nameClaim{name: "bound", standing: dbtype.HostnameFromDHCPStatic, at: at(1)},
		},
		{
			name: "equal standing goes to the later sighting",
			claims: []nameClaim{
				{name: "old", standing: dbtype.HostnameFromDNS, at: at(1)},
				{name: "new", standing: dbtype.HostnameFromDNS, at: at(2)},
			},
			want: nameClaim{name: "new", standing: dbtype.HostnameFromDNS, at: at(2)},
		},
		{
			name: "an unknown standing loses to a known one",
			claims: []nameClaim{
				{name: "unknown", standing: dbtype.HostnameSource("SSDP"), at: at(2)},
				{name: "leased", standing: dbtype.HostnameFromDHCPLease, at: at(1)},
			},
			want: nameClaim{name: "leased", standing: dbtype.HostnameFromDHCPLease, at: at(1)},
		},
		{
			name:   "an unknown standing still beats no name at all",
			claims: []nameClaim{{name: "unknown", standing: dbtype.HostnameSource("SSDP"), at: at(1)}},
			want:   nameClaim{name: "unknown", standing: dbtype.HostnameSource("SSDP"), at: at(1)},
		},
		{
			name: "the best-standing claim wins however it is ordered",
			claims: []nameClaim{
				{name: "resolved", standing: dbtype.HostnameFromDNS, at: at(1)},
				{name: "leased", standing: dbtype.HostnameFromDHCPStatic, at: at(3)},
			},
			want: nameClaim{name: "resolved", standing: dbtype.HostnameFromDNS, at: at(1)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, resolveHostname(tt.claims))
		})
	}
}

func TestSameName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b string
		want bool
	}{
		{a: "host-a", b: "host-a", want: true},
		{a: "host-a.example.com", b: "host-a", want: true},
		{a: "HOST-A.example.com", b: "host-a", want: true},
		{a: "host-a.example.com", b: "host-a.test", want: true},
		{a: "host-a", b: "host-b", want: false},
		{a: "gateway.example.com", b: "host-a.example.com", want: false},
		{a: "", b: "", want: true},

		// A source retracting a name is a change, not two spellings agreeing.
		{a: "host-a", b: "", want: false},
		{a: "", b: "host-a", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.a+"/"+tt.b, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, sameName(tt.a, tt.b))
		})
	}
}
