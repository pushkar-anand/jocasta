package web

import (
	"html/template"
	"slices"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/classify"
)

// classLabel words a device class for a reader. The class values are lower-snake
// identifiers the storage layer keeps; this is the only place they are spelled
// for a person.
func classLabel(c classify.Class) string {
	switch c {
	case classify.Router:
		return "Router"
	case classify.Switch:
		return "Switch"
	case classify.AccessPoint:
		return "Access point"
	case classify.Firewall:
		return "Firewall"
	case classify.Server:
		return "Server"
	case classify.NAS:
		return "NAS"
	case classify.Hypervisor:
		return "Hypervisor"
	case classify.Desktop:
		return "Desktop"
	case classify.Laptop:
		return "Laptop"
	case classify.Phone:
		return "Phone"
	case classify.Tablet:
		return "Tablet"
	case classify.Printer:
		return "Printer"
	case classify.Camera:
		return "Camera"
	case classify.TV:
		return "TV"
	case classify.Streaming:
		return "Media Player"
	case classify.Speaker:
		return "Speaker"
	case classify.VoiceAssistant:
		return "Voice assistant"
	case classify.GameConsole:
		return "Game console"
	case classify.IoTHub:
		return "Smart-home hub"
	case classify.SmartHome:
		return "Smart-home device"
	case classify.Wearable:
		return "Wearable"
	case classify.VoIP:
		return "VoIP phone"
	}

	return ""
}

// confidence words how strong a case the classifier's guess rests on, for the
// reader who sees "auto" beside a type and wants to know how much to trust it.
// Empty for a guess that carries no confidence, which is every Unknown one.
func confidence(c classify.Confidence) string {
	switch c {
	case classify.High:
		return "high confidence"
	case classify.Medium:
		return "some confidence"
	case classify.Low:
		return "low confidence"
	}

	return ""
}

// classChoice is one option of the device-type picker.
type classChoice struct {
	Value classify.Class
	Label string
}

// classChoices is the picker's options, ordered by label: the classifier's own
// order is a tie-break rule, not one a person reading a list would expect. The
// blank "let the classifier decide" option is the template's to add, since only
// it knows the current guess to name in it.
func classChoices() []classChoice {
	classes := classify.Classes()
	out := make([]classChoice, len(classes))

	for i, c := range classes {
		out[i] = classChoice{Value: c, Label: classLabel(c)}
	}

	slices.SortFunc(out, func(a, b classChoice) int {
		return strings.Compare(a.Label, b.Label)
	})

	return out
}

// classGlyphs are the device-class icons: the inner markup of a 24px SVG,
// stroked in currentColor so a surface can size and colour them like the log
// glyphs. This package owns them, which is what makes returning them as HTML
// safe.
var classGlyphs = map[classify.Class]template.HTML{
	classify.Router:         `<rect x="3" y="13" width="18" height="8" rx="1"/><path d="M7 17h.01M11 17h.01"/><path d="M12 13V9a3 3 0 0 1 3-3M12 9a3 3 0 0 0-3-3"/>`,
	classify.Switch:         `<rect x="3" y="8" width="18" height="8" rx="1"/><path d="M7 12h.01M11 12h.01M15 12h.01"/>`,
	classify.AccessPoint:    `<path d="M5 12a7 7 0 0 1 14 0"/><path d="M8.5 12a3.5 3.5 0 0 1 7 0"/><circle cx="12" cy="12" r="1.5"/><path d="M12 13v8"/>`,
	classify.Firewall:       `<rect x="3" y="4" width="18" height="16" rx="1"/><path d="M3 9h18M3 15h18M9 4v5M15 9v6M9 15v5"/>`,
	classify.Server:         `<rect x="4" y="4" width="16" height="7" rx="1"/><rect x="4" y="13" width="16" height="7" rx="1"/><path d="M8 7.5h.01M8 16.5h.01"/>`,
	classify.NAS:            `<rect x="5" y="3" width="14" height="18" rx="1"/><path d="M9 7h6M9 11h6"/><circle cx="12" cy="16" r="1.5"/>`,
	classify.Hypervisor:     `<rect x="3" y="3" width="8" height="8" rx="1"/><rect x="13" y="3" width="8" height="8" rx="1"/><rect x="3" y="13" width="8" height="8" rx="1"/><rect x="13" y="13" width="8" height="8" rx="1"/>`,
	classify.Desktop:        `<rect x="3" y="4" width="18" height="12" rx="1"/><path d="M8 20h8M12 16v4"/>`,
	classify.Laptop:         `<rect x="4" y="5" width="16" height="11" rx="1"/><path d="M2 20h20"/>`,
	classify.Phone:          `<rect x="7" y="2" width="10" height="20" rx="2"/><path d="M11 18h2"/>`,
	classify.Tablet:         `<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M11 17h2"/>`,
	classify.Printer:        `<path d="M6 9V3h12v6"/><rect x="4" y="9" width="16" height="8" rx="1"/><path d="M8 15h8v6H8z"/><path d="M17 12h.01"/>`,
	classify.Camera:         `<rect x="2" y="7" width="14" height="10" rx="1"/><path d="M16 10l5-3v10l-5-3"/>`,
	classify.TV:             `<rect x="2" y="5" width="20" height="12" rx="1"/><path d="M7 21l5-4 5 4"/>`,
	classify.Streaming:      `<rect x="3" y="8" width="18" height="8" rx="2"/><path d="M7 12h4"/><circle cx="16" cy="12" r="1"/>`,
	classify.Speaker:        `<rect x="6" y="3" width="12" height="18" rx="2"/><circle cx="12" cy="14" r="3"/><path d="M12 7h.01"/>`,
	classify.VoiceAssistant: `<rect x="7" y="3" width="10" height="18" rx="5"/><path d="M10 13h4M9.5 16h5"/>`,
	classify.GameConsole:    `<rect x="2" y="7" width="20" height="10" rx="5"/><path d="M7 12h3M8.5 10.5v3"/><circle cx="15.5" cy="11" r="1"/><circle cx="17.5" cy="13" r="1"/>`,
	classify.IoTHub:         `<circle cx="12" cy="12" r="3"/><path d="M12 3v3M12 18v3M3 12h3M18 12h3M6 6l2 2M16 16l2 2M18 6l-2 2M8 16l-2 2"/>`,
	classify.SmartHome:      `<path d="M4 11l8-7 8 7"/><path d="M6 10v9h12v-9"/><circle cx="12" cy="14" r="2"/>`,
	classify.Wearable:       `<rect x="8" y="7" width="8" height="10" rx="2"/><path d="M9.5 7l.7-4h3.6l.7 4M9.5 17l.7 4h3.6l.7-4"/>`,
	classify.VoIP:           `<path d="M5 4h5l1.5 5-2.5 1.8a12 12 0 0 0 5.2 5.2L17 18.5 22 20v-1a2 2 0 0 0-2-2h0M4 4a16 16 0 0 0 16 16"/>`,
}

// classIcon is the glyph for a class, and empty markup for one with none --
// including the zero class, so a template can ask for an icon without first
// checking whether the device has been classified.
func classIcon(c classify.Class) template.HTML {
	return classGlyphs[c]
}
