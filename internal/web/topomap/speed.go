package topomap

import (
	"strconv"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/topology"
)

// rateUnits are the units a rate is said in, largest first.
var rateUnits = []struct {
	size float64
	name string
}{{1e9, "Gbps"}, {1e6, "Mbps"}, {1e3, "kbps"}, {1, "bps"}}

// Rate says a rate in bits per second the way people do: "1 Gbps",
// "2.5 Gbps", "867 Mbps". Zero is unknown and comes back empty.
func Rate(bps int64) string {
	for _, u := range rateUnits {
		if n := float64(bps) / u.size; n >= 1 {
			return figure(n) + " " + u.name
		}
	}

	return ""
}

// figure renders n with one decimal below 10 and none above, so a rate reads
// as "2.5" or "867" and never as "866.6".
func figure(n float64) string {
	if n >= 10 {
		return strconv.FormatFloat(n, 'f', 0, 64)
	}

	return strings.TrimSuffix(strconv.FormatFloat(n, 'f', 1, 64), ".0")
}

// SpeedLabel says how fast a wired link runs, as "1 Gbps", with
// "half duplex" after a link that runs half duplex. An unknown rate comes
// back empty.
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

// RadioLabel says how a Wi-Fi client's connection runs, as
// "867 Mbps down · 650 Mbps up · -54 dBm", where down is what the radio sends
// the client. What is unknown is left out, and a connection with nothing
// known comes back empty.
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
