package topomap

import (
	"strconv"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/topology"
)

// rateUnits are the units of a rate, largest first.
var rateUnits = []struct {
	size float64
	name string
}{{1e9, "Gbps"}, {1e6, "Mbps"}, {1e3, "kbps"}, {1, "bps"}}

// Rate writes a rate in bits per second as people say it: "1 Gbps",
// "2.5 Gbps", "867 Mbps". Zero is unknown, and comes back empty.
func Rate(bps int64) string {
	for _, u := range rateUnits {
		if n := float64(bps) / u.size; n >= 1 {
			return figure(n) + " " + u.name
		}
	}

	return ""
}

// figure renders n with one decimal below 10, and none above. So a rate reads
// as "2.5" or "867", and never as "866.6".
func figure(n float64) string {
	if n >= 10 {
		return strconv.FormatFloat(n, 'f', 0, 64)
	}

	return strings.TrimSuffix(strconv.FormatFloat(n, 'f', 1, 64), ".0")
}

// SpeedLabel tells how fast a wired link runs, as "1 Gbps". It adds
// "half duplex" after a link that runs half duplex. An unknown rate comes back
// empty.
func SpeedLabel(s topology.Speed) string {
	switch {
	case s.Rate == 0:
		return ""
	case s.FullDuplex:
		return Rate(s.Rate)
	default:
		return Rate(s.Rate) + " · half duplex"
	}
}

// RadioLabel tells how the connection of a Wi-Fi client runs, as
// "867 Mbps down · 650 Mbps up · -54 dBm". Down is what the radio sends to the
// client. It leaves out what is unknown. A connection with nothing known comes
// back empty.
func RadioLabel(r topology.Radio) string {
	var parts []string

	if r.Down > 0 {
		parts = append(parts, Rate(r.Down)+" down")
	}

	if r.Up > 0 {
		parts = append(parts, Rate(r.Up)+" up")
	}

	if r.Signal != 0 {
		parts = append(parts, strconv.Itoa(r.Signal)+" dBm")
	}

	return strings.Join(parts, " · ")
}
