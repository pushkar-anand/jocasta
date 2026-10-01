package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
)

// The helpers here are shared by every router source: each reads several
// tables, merges them into one draft per device per address, and builds facts
// from the drafts.

// claimKey keys every table a router reads the same way, so a lease's name
// reaches the neighbour or ARP entry for the same device. As a second claim,
// that entry would overwrite the name with an empty one.
//
// The address is part of it because each address is separately current.
type claimKey struct {
	mac  string
	addr string
}

type claims map[claimKey]*draft

func draftFor(c claims, k claimKey) *draft {
	d, ok := c[k]
	if !ok {
		d = &draft{claimKey: k}
		c[k] = d
	}

	return d
}

// draft accumulates what a router's tables say about one device at one
// address.
type draft struct {
	claimKey

	present  bool
	hostname string
	nameFrom dbtype.HostnameSource
	detail   map[string]string
}

// set skips empty values: a router renders an absent field as one, and a
// detail key with nothing behind it reads as the source having something to
// say.
func (d *draft) set(key, value string) {
	if value == "" {
		return
	}

	if d.detail == nil {
		d.detail = make(map[string]string, 4)
	}

	d.detail[key] = value
}

// buildFacts turns a router's merged drafts into facts stamped seenAt, sorted
// by address.
//
// Name resolution stays off: a nameless row would otherwise come back carrying
// whatever this host's resolver said, filed under the router's claim.
func buildFacts(ctx context.Context, log *slog.Logger, seenAt time.Time, c claims) ([]Fact, error) {
	facts := make([]Fact, 0, len(c))

	var errs []error

	for _, d := range c {
		h, err := hosts.BuildHost(ctx, hosts.HostInput{
			IP:       d.addr,
			MAC:      d.mac,
			Hostname: d.hostname,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("build host %s: %w", d.addr, err))

			continue
		}

		facts = append(facts, Fact{
			Host:           h,
			Present:        d.present,
			HostnameSource: d.nameFrom,
			Detail:         d.detail,
			SeenAt:         seenAt,
		})
	}

	// Map iteration is randomised, and a run that reports its rows in a
	// different sequence every time cannot be diffed against the last one.
	slices.SortFunc(facts, func(a, b Fact) int {
		return cmp.Or(
			a.Host.Address().Compare(b.Host.Address()),
			strings.Compare(a.Host.MAC, b.Host.MAC),
		)
	})

	if len(errs) > 0 {
		log.DebugContext(ctx, "some rows did not build", slog.Int("failed", len(errs)))
	}

	return facts, errors.Join(errs...)
}

// shareByDevice gives every draft for one device the name and detail that all
// of its addresses carry between them.
//
// A device holding two addresses is two facts, but the claim they write is
// keyed on the device alone, so the last one to land decides what the claim
// says. Without this a nameless address clears the name another found, and a
// thinner detail map overwrites a richer one: a device keeps a stale ARP entry
// for an address it has given up, and that address has no lease behind it.
func shareByDevice(c claims) {
	// Sorted, so a key two addresses disagree on resolves the same way every
	// run.
	keys := slices.SortedFunc(maps.Keys(c), func(a, b claimKey) int {
		return cmp.Or(strings.Compare(a.addr, b.addr), strings.Compare(a.mac, b.mac))
	})

	names := make(map[string]*draft, len(c))
	details := make(map[string]map[string]string, len(c))

	for _, k := range keys {
		d := c[k]
		if d.mac == "" {
			continue
		}

		if d.hostname != "" {
			if best, ok := names[d.mac]; !ok || d.nameFrom.Rank() > best.nameFrom.Rank() {
				names[d.mac] = d
			}
		}

		merged, ok := details[d.mac]
		if !ok {
			merged = make(map[string]string, len(d.detail))
			details[d.mac] = merged
		}

		// Union: detail is per address and the claim is per device, so only
		// the union loses nothing. Where two addresses
		// disagree the lower one wins, which is arbitrary but stable.
		for key, value := range d.detail {
			if _, seen := merged[key]; !seen {
				merged[key] = value
			}
		}
	}

	for _, d := range c {
		if best, ok := names[d.mac]; ok {
			d.hostname = best.hostname
			d.nameFrom = best.nameFrom
		}

		// Cloned, so two facts sharing a device do not share a mutable map.
		if merged, ok := details[d.mac]; ok {
			d.detail = maps.Clone(merged)
		}
	}
}

// segment reads the prefix an address sits on, masked to its base.
//
// Loopback and link-local prefixes are dropped: the router holds addresses on
// both and neither is a segment anything is inventoried on.
func segment(s string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, false
	}

	if p.Addr().IsLoopback() || p.Addr().IsLinkLocalUnicast() {
		return netip.Prefix{}, false
	}

	return p.Masked(), true
}
