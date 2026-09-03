package classify

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRulesetIsWellFormed checks the invariants a rule has to hold, so a
// malformed entry fails here rather than misclassifying quietly.
func TestRulesetIsWellFormed(t *testing.T) {
	t.Parallel()

	for i, r := range ruleset {
		assert.Truef(t, positiveConds(r.Cond) > 0, "rule %d (%s) has no condition -- it would match everything", i, r.Class)
		assert.Truef(t, r.Class.Valid() && r.Class != Unknown, "rule %d has an unknown class %q", i, r.Class)
		assert.Truef(t, r.Reason != "" || r.ReasonFn != nil, "rule %d (%s) has no reason", i, r.Class)

		for _, pat := range []string{r.Host, r.HostNot} {
			if pat != "" {
				_, err := regexp.Compile(pat)
				assert.NoErrorf(t, err, "rule %d has a bad pattern %q", i, pat)
			}
		}
	}
}

// TestNoDuplicateConditions catches a rule pasted twice: the later copy can
// never win, since the earlier one matches the same inputs and is listed first.
func TestNoDuplicateConditions(t *testing.T) {
	t.Parallel()

	seen := map[string]int{}

	for i, r := range ruleset {
		key := r.Vendor + "|" + r.Host + "|" + r.HostNot + "|" + r.Network

		for _, p := range r.AnyPort {
			key += "|any" + string(rune(p))
		}

		for _, p := range r.AllPort {
			key += "|all" + string(rune(p))
		}

		key += "|" + string(rune(r.Port))

		if r.Randomised != nil {
			key += "|rand"
		}

		// two port rules for one port that vote different classes are allowed
		// (7000 and 5555 do this on purpose); tag the class in only that case.
		if r.Port != 0 && positiveConds(r.Cond) == 1 {
			key += "|" + string(r.Class)
		}

		if first, ok := seen[key]; ok {
			t.Errorf("rule %d duplicates the conditions of rule %d", i, first)
		}

		seen[key] = i
	}
}

// positiveConds counts the condition fields set on c, ignoring the HostNot
// veto. It mirrors what match reports for a rule that fires.
func positiveConds(c Cond) int {
	n := 0

	for _, set := range []bool{
		c.Vendor != "", c.Host != "", c.Network != "",
		c.Port != 0, len(c.AnyPort) > 0, len(c.AllPort) > 0,
		c.Randomised != nil, c.MinServer > 0, c.When != nil,
	} {
		if set {
			n++
		}
	}

	return n
}

func TestFirstMatchAndSpecificity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   Input
		want Class
	}{
		{
			name: "a pickier rule beats a broad one regardless of order",
			// Espressif alone is smart_home; the ESP name is too. A server VLAN
			// would drag a lone weak signal, but the specific name holds.
			in:   Input{Vendor: "Espressif Inc.", Hostname: "esp-a1b2c3", NetworkName: "lab-servers"},
			want: SmartHome,
		},
		{
			name: "a specific hostname outranks the randomised-address guess",
			in:   Input{Hostname: "sarah-thinkpad-x1", Randomised: true},
			want: Laptop,
		},
		{
			name: "underscored smart-bulb names match (the old \\b bug)",
			in:   Input{Vendor: "WiZ", Hostname: "wiz_a1b2c3"},
			want: SmartHome,
		},
		{
			name: "a lone weak vendor still yields a guess",
			in:   Input{Vendor: "Dell Inc."},
			want: Desktop,
		},
		{
			name: "Home Assistant's port wins over a bare service-port count",
			in:   Input{OpenPorts: []uint16{8123, 22, 3000}},
			want: IoTHub,
		},
		{
			name: "a Proxmox OUI reads as a server, even beside a cast port",
			in:   Input{Vendor: "Proxmox Server Solutions GmbH", OpenPorts: []uint16{22, 8009, 9100}},
			want: Server,
		},
		{
			name: "a smart-speaker name beats its own cast ports",
			in:   Input{Vendor: "Google, Inc.", Hostname: "kitchen-nest-audio", OpenPorts: []uint16{8008, 8009}},
			want: Speaker,
		},
		{
			name: "cast ports with no telling name are a media player",
			in:   Input{OpenPorts: []uint16{8008, 8009}},
			want: Streaming,
		},
		{
			name: "port 9100 on its own is a node-exporter",
			in:   Input{OpenPorts: []uint16{9100}},
			want: Server,
		},
		{
			name: "a bare Intel OUI falls back to desktop",
			in:   Input{Vendor: "Intel Corporate"},
			want: Desktop,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, Device(tt.in).Class)
		})
	}
}
