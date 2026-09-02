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

// standingRank orders the ways a name can be learned. Reverse DNS comes first
// because it is the name that resolves: a DHCP host-name is the client's own
// claim about itself, static lease or not, and may resolve to nothing. Where
// the resolver serves the router's leases the PTR is that same name with the
// domain attached, so preferring DNS keeps the fuller spelling.
//
// An unknown standing ranks zero, so a known name still beats an unknown one.
func standingRank(s dbtype.HostnameSource) int {
	switch s {
	case dbtype.HostnameFromDNS:
		return 3
	case dbtype.HostnameFromDHCPStatic:
		return 2
	case dbtype.HostnameFromDHCPLease:
		return 1
	default:
		return 0
	}
}

// resolveHostname elects the name a device row carries from every source's
// claim, and reports the zero claim when no source offers one.
//
// Ties go to the later sighting: two sources of equal standing disagreeing is a
// device that was renamed. Equal standing at equal times keeps input order,
// which ListDeviceSources fixes.
//
// A ranking in Go rather than a CASE in SQL, because the constants and the
// query would be two places to keep in step and the device page needs this read
// anyway.
func resolveHostname(claims []nameClaim) nameClaim {
	var (
		won  nameClaim
		rank = -1
	)

	for _, c := range claims {
		if c.name == "" {
			continue
		}

		r := standingRank(c.standing)
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
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}

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
