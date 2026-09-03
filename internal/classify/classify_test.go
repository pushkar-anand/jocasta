package classify_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/classify"
)

func TestDevice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		in         classify.Input
		want       classify.Class
		confidence classify.Confidence
	}{
		{
			name: "nothing known",
			in:   classify.Input{},
			want: classify.Unknown,
		},
		{
			name:       "a printer vendor alone",
			in:         classify.Input{Vendor: "Brother Industries, Ltd."},
			want:       classify.Printer,
			confidence: classify.Medium,
		},
		{
			name:       "a broad vendor alone is a weak guess",
			in:         classify.Input{Vendor: "Apple, Inc."},
			want:       classify.Phone,
			confidence: classify.Low,
		},
		{
			name:       "the name says iPhone",
			in:         classify.Input{Hostname: "Alex-iPhone"},
			want:       classify.Phone,
			confidence: classify.Medium,
		},
		{
			name:       "vendor and name and a randomised address agree",
			in:         classify.Input{Vendor: "Apple, Inc.", Hostname: "alex-iphone", Randomised: true},
			want:       classify.Phone,
			confidence: classify.High,
		},
		{
			name:       "a MacBook name outweighs the randomised-address phone hint",
			in:         classify.Input{Hostname: "workshop-macbook-pro", Randomised: true},
			want:       classify.Laptop,
			confidence: classify.Medium,
		},
		{
			name:       "a chromecast port",
			in:         classify.Input{OpenPorts: []uint16{8009}},
			want:       classify.Streaming,
			confidence: classify.Medium,
		},
		{
			name:       "print ports and a print vendor",
			in:         classify.Input{Vendor: "Hewlett Packard", Hostname: "HPB2A1C4", OpenPorts: []uint16{515, 631, 9100}},
			want:       classify.Printer,
			confidence: classify.High,
		},
		{
			name:       "9100 alone is only a hint",
			in:         classify.Input{OpenPorts: []uint16{9100}},
			want:       classify.Printer,
			confidence: classify.Low,
		},
		{
			name:       "9100 next to server ports is a node-exporter, not a printer",
			in:         classify.Input{OpenPorts: []uint16{9100, 9090, 3000, 22}},
			want:       classify.Server,
			confidence: classify.Medium,
		},
		{
			name:       "a NAS vendor and its ports",
			in:         classify.Input{Vendor: "Synology Incorporated", OpenPorts: []uint16{5001, 548, 2049}},
			want:       classify.NAS,
			confidence: classify.High,
		},
		{
			name:       "Proxmox by its port",
			in:         classify.Input{OpenPorts: []uint16{8006, 22}},
			want:       classify.Hypervisor,
			confidence: classify.Medium,
		},
		{
			name:       "an ESP node on the IoT VLAN",
			in:         classify.Input{Vendor: "Espressif Inc.", Hostname: "esp-a41f2c", NetworkName: "IoT"},
			want:       classify.SmartHome,
			confidence: classify.High,
		},
		{
			name:       "an RTSP stream on the cameras VLAN",
			in:         classify.Input{OpenPorts: []uint16{554}, NetworkName: "cameras"},
			want:       classify.Camera,
			confidence: classify.Medium,
		},
		{
			name:       "a game console by name",
			in:         classify.Input{Hostname: "Xbox-System-OS"},
			want:       classify.GameConsole,
			confidence: classify.Medium,
		},
		{
			name:       "a randomised address and nothing else",
			in:         classify.Input{Randomised: true},
			want:       classify.Phone,
			confidence: classify.Low,
		},
		{
			name:       "a UniFi AP: weak vendor, strong controller port",
			in:         classify.Input{Vendor: "Ubiquiti Inc", OpenPorts: []uint16{6789}},
			want:       classify.AccessPoint,
			confidence: classify.Medium,
		},
		{
			name:       "an unrecognised vendor and a bare web port",
			in:         classify.Input{Vendor: "Some Unlisted OEM", OpenPorts: []uint16{80, 443}},
			want:       classify.Unknown,
			confidence: classify.NoConfidence,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := classify.Device(tt.in)

			assert.Equal(t, tt.want, got.Class)
			assert.Equal(t, tt.confidence, got.Confidence)

			if tt.want == classify.Unknown {
				assert.Empty(t, got.Reasons)
			} else {
				assert.NotEmpty(t, got.Reasons, "a classified device should say why")
			}
		})
	}
}

// TestDeviceIsDeterministic guards the tie-breaking: the same input, classified
// repeatedly, must give the same class and the same reasons in the same order.
func TestDeviceIsDeterministic(t *testing.T) {
	t.Parallel()

	in := classify.Input{
		Vendor:      "Apple, Inc.",
		Hostname:    "media-room-apple-tv",
		OpenPorts:   []uint16{7000, 554, 8009, 22, 3306},
		NetworkName: "living-room",
		Randomised:  true,
	}

	first := classify.Device(in)

	for range 50 {
		got := classify.Device(in)
		require.Equal(t, first, got)
	}
}

// TestDeviceIgnoresPortOrderAndDuplicates checks the input is normalised before
// the rules see it.
func TestDeviceIgnoresPortOrderAndDuplicates(t *testing.T) {
	t.Parallel()

	a := classify.Device(classify.Input{OpenPorts: []uint16{515, 631, 9100}})
	b := classify.Device(classify.Input{OpenPorts: []uint16{9100, 9100, 631, 515, 631}})

	assert.Equal(t, a, b)
}

func TestDeviceReasonsNameTheWinningSignals(t *testing.T) {
	t.Parallel()

	got := classify.Device(classify.Input{
		Vendor:    "Synology Incorporated",
		OpenPorts: []uint16{5001, 32400}, // 32400 votes Server, not NAS
	})

	require.Equal(t, classify.NAS, got.Class)

	for _, r := range got.Reasons {
		assert.NotContains(t, r, "Plex", "a reason for NAS should not cite the Server signal")
	}
}

// TestDeviceMatchesTruncatedVendorNames checks the vendor rule against the forms
// the OUI table actually stores: the registered name, and the truncated
// Wireshark form. It also checks a prefix does not bleed into an unrelated name.
func TestDeviceMatchesTruncatedVendorNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		vendor string
		want   classify.Class
	}{
		{"HewlettPacka", classify.Printer},               // truncated Wireshark form
		{"Hewlett Packard Enterprise", classify.Printer}, // registered form
		{"BrotherIndus", classify.Printer},
		{"Synology", classify.NAS},
		{"RaspberryPiF", classify.Server},
		{"AmazonTechno", classify.VoiceAssistant},
		{"Espressif Inc.", classify.SmartHome},
		{"HandlinkTechnology", classify.Unknown}, // substring "dlink" is present but not a prefix
		{"Some Unlisted OEM", classify.Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.vendor, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, classify.Device(classify.Input{Vendor: tt.vendor}).Class)
		})
	}
}

func TestClassValid(t *testing.T) {
	t.Parallel()

	assert.True(t, classify.Unknown.Valid())
	assert.True(t, classify.Printer.Valid())
	assert.False(t, classify.Class("toaster").Valid())

	for _, c := range classify.Classes() {
		assert.True(t, c.Valid())
		assert.NotEqual(t, classify.Unknown, c, "Classes() must not include Unknown")
	}
}
