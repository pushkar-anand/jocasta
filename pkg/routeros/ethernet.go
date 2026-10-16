package routeros

import (
	"context"
	"strings"
)

// Link is how the link of one Ethernet or SFP port came up, as the Ethernet
// monitor reports it.
type Link struct {
	Name string `json:"name"`

	// Rate is the speed that the two ends agreed on. It is zero without a
	// link.
	Rate       Rate `json:"rate"`
	FullDuplex Bool `json:"full-duplex"`

	// Advertising lists the modes that this port offers, as
	// "1G-baseT-full,2.5G-baseT". PartnerAdvertising lists the modes that the
	// other end offers. The partner list is empty on a port that does not
	// auto-negotiate, such as most SFP modules.
	Advertising        string `json:"advertising"`
	PartnerAdvertising string `json:"link-partner-advertising"`
}

// Capable returns the fastest rate that both ends advertise. It returns zero
// when one end advertises nothing. If Rate is below it, the link came up
// slower than both ends support. A worn cable or a loose plug usually causes
// this.
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

// Links reads the link on each enabled Ethernet and SFP port in ifaces. The
// Ethernet monitor reports only on these kinds of interface.
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

	// once makes the monitor answer one time. Without it, the console prints
	// until it is interrupted.
	rows, err := r.post[[]Link](ctx, ethernetMonitorAPI, map[string]string{
		"numbers": strings.Join(ports, ","),
		"once":    "",
	})
	if err != nil {
		return nil, err
	}

	return *rows, nil
}
