package inventory

import (
	"strings"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// nameClaim is one source's say on what a device is called, reduced to what the
// election weighs.
type nameClaim struct {
	name     string
	standing dbtype.HostnameSource
	at       dbtype.Time
}

// resolveHostname elects the name a device row carries from every source's
// claim, and reports the zero claim when no source offers one.
//
// Ties go to the later sighting: two sources of equal standing disagreeing is a
// device that was renamed. Equal standing at equal times keeps input order,
// which ListDeviceSources fixes.
//
// The ranking is in Go because a CASE in SQL would be a second place to keep in
// step with the constants, and the device page needs this read anyway.
func resolveHostname(claims []nameClaim) nameClaim {
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

		if r > rank || won.at.Before(c.at.Time) {
			won, rank = c, r
		}
	}

	return won
}

// label reduces a name to its first label, case-folded, which is as much as two
// sources can be compared on: a sweep keeps the whole PTR (host-a.example.com)
// while a DHCP lease carries the bare label the client announced (host-a). Where
// the resolver answers from the router's own leases those are one name in two
// spellings, and comparing them whole makes almost every device look like two
// sources disagreeing.
//
// Two different devices sharing a first label under different domains therefore
// read as agreeing. Their claims are kept either way.
func label(name string) string {
	name, _, _ = strings.Cut(name, ".")

	return strings.ToLower(name)
}

// sameName reports whether two names refer to one device, allowing for the
// spelling difference label describes.
func sameName(a, b string) bool {
	if a == b {
		return true
	}

	// Retracting a name and holding one whose first label is empty are not the
	// same event.
	if a == "" || b == "" {
		return false
	}

	return label(a) == label(b)
}
