package routeros

import "fmt"

// Bool is a RouterOS boolean.
//
// The REST service renders most values as strings, so a flag arrives as
// "true" rather than true. Some fields and some versions send a real JSON
// boolean, and an absent flag sends nothing at all, so all three decode and
// the missing one is false -- which is what every flag on these tables means
// by its absence.
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
