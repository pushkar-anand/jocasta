package web

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

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

	noun := "port "
	if a.PortCount > 1 {
		noun = "ports "
	}

	out := proto + noun + portList(a.Ports)
	if more := a.PortCount - int64(len(a.Ports)); more > 0 {
		out += fmt.Sprintf(" +%d more", more)
	}

	return out
}

// filterAttempts keeps the tries on f's service whose names contain its
// query.
func filterAttempts(attempts []*inventory.Attempt, f trafficFilter) []*inventory.Attempt {
	query := strings.ToLower(f.Query)
	protocol, port, hasService := parseServiceKey(f.Service)

	var out []*inventory.Attempt

	for _, a := range attempts {
		switch {
		case hasService && (a.Protocol != protocol || (port != 0 && !slices.Contains(a.Ports, port))):
			continue
		case query != "" && !attemptMatches(a, query):
			continue
		}

		out = append(out, a)
	}

	return out
}

func attemptMatches(a *inventory.Attempt, query string) bool {
	return anyContains(query, a.PeerDeviceName, a.IP.String(), a.OrgShort, attemptWhat(a))
}

// anyContains reports whether any field, lowercased, contains query, which the
// caller has lowercased already.
func anyContains(query string, fields ...string) bool {
	return slices.ContainsFunc(fields, func(s string) bool {
		return strings.Contains(strings.ToLower(s), query)
	})
}

// portList is ports as a person reads them, "22, 23, 80".
func portList(ports []uint16) string {
	names := make([]string, len(ports))
	for i, p := range ports {
		names[i] = strconv.Itoa(int(p))
	}

	return strings.Join(names, ", ")
}
