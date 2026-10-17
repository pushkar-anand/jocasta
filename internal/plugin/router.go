package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
)

// Every router source shares the helpers here. Each source reads several
// tables. It merges them into one draft for each device and address. It then
// builds facts from the drafts.

// claimKey is the same key for every table that a router reads. So the name of
// a lease reaches the neighbour or ARP entry of the same device. As a second
// claim, that entry would overwrite the name with an empty name.
//
// The address is part of the key, because each address is current
// separately.
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

// draft collects what the tables of a router say about one device at one
// address.
type draft struct {
	claimKey

	present  bool
	hostname string
	nameFrom dbtype.HostnameSource
	detail   map[string]string
}

// set skips empty values. A router renders an absent field as an empty value.
// A detail key with no value would look like the source has something to
// tell.
func (d *draft) set(key, value string) {
	if value == "" {
		return
	}

	if d.detail == nil {
		d.detail = make(map[string]string, 4)
	}

	d.detail[key] = value
}

// buildFacts turns the merged drafts of a router into facts at seenAt, sorted
// by address.
//
// Name resolution stays off. Otherwise, a row with no name would get the name
// from this host's resolver, under the claim of the router.
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

	// Map iteration is random. A run that reports its rows in a different
	// order every time cannot be compared with the last run.
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

// segment reads the prefix of an address, masked to its base.
//
// segment drops loopback and link-local prefixes. The router has addresses on
// both. Neither is a segment of the inventory.
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
