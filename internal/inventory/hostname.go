package inventory

import (
	"strings"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// nameClaim is the name that one source gives a device, with only the fields
// that the election uses.
type nameClaim struct {
	name     string
	standing dbtype.HostnameSource
	at       dbtype.Time
}

// resolveHostname elects the name of a device row from every claim. It reports
// the zero claim when no claim gives a name. current is the current name of
// the row.
//
// If two claims have the same standing, current stays while a claim still
// gives it. A device can have two addresses with different names, such as a
// router with a PTR for each VLAN. Without this rule, the last read would
// rename it. Among the others, the later sighting wins. If two claims with the
// same standing disagree, the device got a new name. With the same standing
// and the same time, input order stays. ListDeviceSources fixes that order.
//
// The ranking is in Go. A CASE in SQL would be a second place to keep equal to
// the constants. Also, the device page needs this read.
func resolveHostname(claims []nameClaim, current string) nameClaim {
	var (
		won  nameClaim
		rank = -1
	)

	for _, c := range claims {
		if c.name == "" {
			continue
		}

		r := c.standing.Rank()
		if r < rank {
			continue
		}

		if r > rank || keeps(c.name, won.name, current) ||
			!keeps(won.name, c.name, current) && won.at.Before(c.at.Time) {
			won, rank = c, r
		}
	}

	return won
}

// keeps reports whether name, and not other, is the current name.
func keeps(name, other, current string) bool {
	return sameName(name, current) && !sameName(other, current)
}

// label reduces a name to its first label, with case folded. Two sources can
// compare only this much. A sweep keeps the whole PTR (host-a.example.com). A
// DHCP lease has only the label that the client announced (host-a). If the
// resolver answers from the leases of the router, these are one name in two
// spellings. A comparison of the whole names would make almost every device
// look like two sources that disagree.
//
// So two different devices with the same first label under different domains
// agree. The store keeps their claims in both cases.
func label(name string) string {
	name, _, _ = strings.Cut(name, ".")

	return strings.ToLower(name)
}

// sameName reports whether two names refer to one device. It allows the
// spelling difference that label describes.
func sameName(a, b string) bool {
	if a == b {
		return true
	}

	// The removal of a name is a different event from a name with an empty
	// first label.
	if a == "" || b == "" {
		return false
	}

	return label(a) == label(b)
}
