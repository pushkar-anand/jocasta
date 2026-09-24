package web

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// attemptEntry is one row of a device's "Tried, no conversation" list: a
// peer on its own, or unknown local addresses collapsed by subnet -- a sweep
// of a whole /24 is one row that opens to the addresses behind it.
type attemptEntry struct {
	Peer  *inventory.Attempt
	Group *attemptGroup
}

type attemptGroup struct {
	Prefix             netip.Prefix
	Peers              []*inventory.Attempt
	Attempts, Answered int64
	LastHour           time.Time
}

func (e *attemptEntry) attempts() int64 {
	if e.Group != nil {
		return e.Group.Attempts
	}

	return e.Peer.Attempts
}

// attemptWhat says what was tried: pings, or which ports.
func attemptWhat(a *inventory.Attempt) string {
	switch a.Protocol {
	case 1, 58:
		return "ping"
	}

	proto := protoName(a.Protocol)
	if proto != "" {
		proto += " "
	}

	if len(a.Ports) == 0 {
		return strings.TrimSpace(proto)
	}

	names := make([]string, len(a.Ports))
	for i, p := range a.Ports {
		names[i] = strconv.Itoa(int(p))
	}

	noun := "port "
	if a.PortCount > 1 {
		noun = "ports "
	}

	out := proto + noun + strings.Join(names, ", ")
	if more := a.PortCount - int64(len(a.Ports)); more > 0 {
		out += fmt.Sprintf(" +%d more", more)
	}

	return out
}

// groupAttempts applies f to a device's attempts and groups what is left.
func groupAttempts(attempts []*inventory.Attempt, f trafficFilter) []*attemptEntry {
	query := strings.ToLower(f.Query)
	protocol, port, hasService := parseServiceKey(f.Service)

	var (
		out   []*attemptEntry
		byNet = make(map[netip.Prefix]*attemptGroup)
		order []netip.Prefix
	)

	for _, a := range attempts {
		switch {
		case f.Scope == trafficScopeLocal && a.Internet,
			f.Scope == trafficScopeInternet && !a.Internet:
			continue
		case hasService && (a.Protocol != protocol || (port != 0 && !slices.Contains(a.Ports, port))):
			continue
		case query != "" && !attemptMatches(a, query):
			continue
		}

		if a.PeerDeviceID != 0 || a.Internet {
			out = append(out, &attemptEntry{Peer: a})

			continue
		}

		bits := 24
		if a.IP.Is6() {
			bits = 64
		}

		pfx, err := a.IP.Prefix(bits)
		if err != nil {
			out = append(out, &attemptEntry{Peer: a})

			continue
		}

		g, ok := byNet[pfx]
		if !ok {
			g = &attemptGroup{Prefix: pfx}
			byNet[pfx] = g
			order = append(order, pfx)
		}

		g.Peers = append(g.Peers, a)
		g.Attempts += a.Attempts
		g.Answered += a.Answered

		if a.LastHour.After(g.LastHour) {
			g.LastHour = a.LastHour
		}
	}

	for _, pfx := range order {
		g := byNet[pfx]
		if len(g.Peers) == 1 {
			out = append(out, &attemptEntry{Peer: g.Peers[0]})

			continue
		}

		out = append(out, &attemptEntry{Group: g})
	}

	slices.SortStableFunc(out, func(a, b *attemptEntry) int { return cmp.Compare(b.attempts(), a.attempts()) })

	return out
}

func attemptMatches(a *inventory.Attempt, query string) bool {
	for _, s := range []string{a.PeerDeviceName, a.IP.String(), a.OrgShort, attemptWhat(a)} {
		if strings.Contains(strings.ToLower(s), query) {
			return true
		}
	}

	return false
}

// proberFor is id's entry among probers, nil when it did not probe.
func proberFor(probers []*inventory.Prober, id int64) *inventory.Prober {
	for _, p := range probers {
		if p.DeviceID == id {
			return p
		}
	}

	return nil
}

// portList is ports as a person reads them, "22, 23, 80".
func portList(ports []uint16) string {
	names := make([]string, len(ports))
	for i, p := range ports {
		names[i] = strconv.Itoa(int(p))
	}

	return strings.Join(names, ", ")
}
