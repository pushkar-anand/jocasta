package routeros

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Bool is a RouterOS boolean.
//
// The REST service renders most values as strings, so a flag arrives as the
// string "true". Some fields and some versions send a real JSON boolean, and
// an absent flag sends nothing at all. All three decode, and a missing flag
// is false, which is what every flag on these tables means by its absence.
type Bool bool

// UnmarshalJSON decodes the router's rendering of a boolean.
func (b *Bool) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case `"true"`, "true", `"yes"`:
		*b = true
	case `"false"`, "false", `"no"`, `""`, "null":
		*b = false
	default:
		return fmt.Errorf("routeros: %s is not a boolean", data)
	}

	return nil
}

// MarshalJSON renders b the way the router would, so a decoded row round-trips
// to the same shape it arrived in.
func (b Bool) MarshalJSON() ([]byte, error) {
	if b {
		return []byte(`"true"`), nil
	}

	return []byte(`"false"`), nil
}

// Rate is a speed in bits per second. Zero is unknown.
//
// The router renders a rate three ways: a bare count ("54000000") in the wifi
// package, a number and unit ("1Gbps", "2.5G") on an Ethernet port, and a
// number and unit followed by how the radio got there
// ("866.6Mbps-80MHz/2S/SGI") in the wireless package. All three decode. A rate
// in any other shape decodes to zero, so one odd row costs its rate and
// leaves the rest of the table readable.
type Rate int64

// UnmarshalJSON decodes the router's rendering of a rate.
func (r *Rate) UnmarshalJSON(data []byte) error {
	*r = ParseRate(strings.Trim(string(data), `"`))

	return nil
}

// ParseRate reads a rate in any of the forms [Rate] describes, and in the
// form a port advertises a mode ("2.5G-baseT"). It returns zero for anything
// else.
func ParseRate(s string) Rate {
	end := strings.IndexFunc(s, func(c rune) bool { return (c < '0' || c > '9') && c != '.' })
	if end < 0 {
		end = len(s)
	}

	n, err := strconv.ParseFloat(s[:end], 64)
	if err != nil || n < 0 {
		return 0
	}

	unit := 1.0

	if end < len(s) {
		switch s[end] {
		case 'k', 'K':
			unit = 1e3
		case 'M':
			unit = 1e6
		case 'G':
			unit = 1e9
		case 'b', '-', ' ':
		default:
			return 0
		}
	}

	return Rate(math.Round(n * unit))
}
