package openwrt

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ipCommand is the command the neighbour table is read with. LuCI's routes page
// runs it the same way, so the ACL group that page uses already allows it.
const ipCommand = "/sbin/ip"

// Neighbour states, as ip prints them. Only these are worth naming: the rest
// ("DELAY", "PROBE", "FAILED", "INCOMPLETE", "NOARP") mean the router has not
// heard from the address lately or never resolved it.
const (
	NeighReachable = "REACHABLE"
	NeighStale     = "STALE"
	NeighPermanent = "PERMANENT"
	NeighFailed    = "FAILED"
)

// Neighbour is one row of the router's neighbour table: an address it has
// resolved, or tried to resolve, to a hardware address. The router is the
// gateway for every segment it serves, so the table covers segments a sweep
// from one host cannot see.
type Neighbour struct {
	// Address is the IPv4 or IPv6 address, as ip printed it.
	Address string

	// Device is the interface the entry was learned on, such as "br-lan" or
	// "br-lan.20".
	Device string

	// MAC is the hardware address, and is empty for an address that never
	// resolved.
	MAC string

	// State is the neighbour state, such as [NeighReachable].
	State string
}

// Usable reports whether the entry names a device: resolved to a hardware
// address and not failed. [Neighbour.Reachable] says whether that device is
// answering now.
func (n Neighbour) Usable() bool {
	return n.MAC != "" && n.State != NeighFailed
}

// Reachable reports whether the router has confirmed the address answers
// within its reachable time. A stale entry is the router remembering a
// hardware address, and a permanent one is configuration, so neither makes a
// device read as online.
func (n Neighbour) Reachable() bool {
	return n.MAC != "" && n.State == NeighReachable
}

// execResult is what `file exec` answers with.
type execResult struct {
	Code   int    `json:"code"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// ErrCommand is a command the router ran and that failed.
var ErrCommand = errors.New("openwrt: command failed")

// exec runs command with params on the router and returns what it printed.
func (o *OpenWrt) exec(ctx context.Context, command string, params ...string) (string, error) {
	res, err := call[execResult](ctx, o, "file", "exec", map[string]any{
		"command": command,
		"params":  params,
	})
	if err != nil {
		return "", err
	}

	if res.Code != 0 {
		return "", fmt.Errorf("%w: %s %s exited %d: %s",
			ErrCommand, command, strings.Join(params, " "), res.Code, strings.TrimSpace(res.Stderr))
	}

	return res.Stdout, nil
}

// Neighbours returns the router's IPv4 and IPv6 neighbour tables. One family
// failing does not cost the other, so a partial read returns its rows
// alongside the error.
func (o *OpenWrt) Neighbours(ctx context.Context) ([]Neighbour, error) {
	var (
		out  []Neighbour
		errs []error
	)

	for _, family := range []string{"-4", "-6"} {
		text, err := o.exec(ctx, ipCommand, family, "neigh", "show")
		if err != nil {
			errs = append(errs, fmt.Errorf("neighbours %s: %w", family, err))

			continue
		}

		out = append(out, ParseNeighbours(text)...)
	}

	return out, errors.Join(errs...)
}

// ParseNeighbours reads the output of `ip neigh show`, from either busybox or
// iproute2, such as:
//
//	192.0.2.10 dev br-lan lladdr 00:00:5e:00:53:01 ref 1 used 0/0/0 probes 4 REACHABLE
//	192.0.2.11 dev br-lan  used 0/0/0 probes 3 FAILED
//	fe80::1 dev br-lan lladdr 00:00:5e:00:53:02 router STALE
//
// A line it cannot read is skipped.
func ParseNeighbours(text string) []Neighbour {
	var out []Neighbour

	for line := range strings.Lines(text) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		n := Neighbour{Address: fields[0]}

		for i := 1; i+1 < len(fields); i++ {
			switch fields[i] {
			case "dev":
				n.Device = fields[i+1]
			case "lladdr":
				n.MAC = fields[i+1]
			}
		}

		// The state is the last word, in capitals. A line without one, as
		// busybox prints for a few kernel states, says nothing about the
		// address.
		last := fields[len(fields)-1]
		if last != strings.ToUpper(last) || strings.ContainsAny(last, "0123456789/:") {
			continue
		}

		n.State = last

		out = append(out, n)
	}

	return out
}
