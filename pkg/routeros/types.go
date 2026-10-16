package routeros

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Bool is a RouterOS boolean.
//
// The REST service renders most values as strings. So a flag arrives as the
// string "true". Some fields and some versions send a real JSON boolean. An
// absent flag sends nothing. All three decode. A missing flag is false. On
// these tables, an absent flag always means false.
type Bool bool

// UnmarshalJSON decodes a boolean as the router renders it.
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

// MarshalJSON renders b as the router does. So a decoded row encodes back to
// the shape in which it arrived.
func (b Bool) MarshalJSON() ([]byte, error) {
	if b {
		return []byte(`"true"`), nil
	}

	return []byte(`"false"`), nil
}

// Rate is a speed in bits per second. Zero is unknown.
//
// The router renders a rate in three ways:
//   - a bare count ("54000000") in the wifi package
//   - a number and unit ("1Gbps", "2.5G") on an Ethernet port
//   - a number and unit, then how the radio got that rate
//     ("866.6Mbps-80MHz/2S/SGI"), in the wireless package
//
// All three decode. A rate in any other shape decodes to zero. So an odd row
// loses only its rate, and the rest of the table is still readable.
type Rate int64

// UnmarshalJSON decodes a rate as the router renders it.
func (r *Rate) UnmarshalJSON(data []byte) error {
	*r = ParseRate(strings.Trim(string(data), `"`))

	return nil
}

// ParseRate reads a rate in each form that [Rate] describes. It also reads the
// form in which a port advertises a mode ("2.5G-baseT"). It returns zero for
// all other forms.
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
