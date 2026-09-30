package routeros

import (
	"context"
	"strings"
)

// Link is how one Ethernet or SFP port's link came up, as the Ethernet
// monitor reports it.
type Link struct {
	Name string `json:"name"`

	// Rate is the speed the two ends agreed on, zero without a link.
	Rate       Rate `json:"rate"`
	FullDuplex Bool `json:"full-duplex"`

	// Advertising lists the modes this port offers and PartnerAdvertising the
	// modes the other end offers, as "1G-baseT-full,2.5G-baseT". The
	// partner's list is empty on a port that does not auto-negotiate, which
	// includes most SFP modules.
	Advertising        string `json:"advertising"`
	PartnerAdvertising string `json:"link-partner-advertising"`
}

// Capable returns the fastest rate both ends advertise, and zero when either
// end advertises nothing. A link whose Rate is below it came up slower than
// both ends support, which a worn cable or a loose plug usually causes.
func (l Link) Capable() Rate {
	return min(fastest(l.Advertising), fastest(l.PartnerAdvertising))
}

// fastest returns the fastest mode in a comma-separated list of advertised
// modes.
func fastest(modes string) Rate {
	var best Rate

	for m := range strings.SplitSeq(modes, ",") {
		best = max(best, ParseRate(strings.TrimSpace(m)))
	}

	return best
}

// Links reads the link on each enabled Ethernet and SFP port among ifaces,
// the kinds of interface the Ethernet monitor reports on.
func (r *RouterOS) Links(ctx context.Context, ifaces []Interface) ([]Link, error) {
	var ports []string

	for _, i := range ifaces {
		if i.Type == "ether" && !i.Disabled {
			ports = append(ports, i.Name)
		}
	}

	if len(ports) == 0 {
		return nil, nil
	}

	// once makes the monitor answer a single time, where the console would
	// keep printing until interrupted.
	rows, err := r.post[[]Link](ctx, ethernetMonitorAPI, map[string]string{
		"numbers": strings.Join(ports, ","),
		"once":    "",
	})
	if err != nil {
		return nil, err
	}

	return *rows, nil
}
