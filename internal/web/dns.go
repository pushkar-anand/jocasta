package web

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"golang.org/x/net/publicsuffix"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// dnsDomainRows is how many domains the section lists. The rest are counted
// in the summary and in a line under the table.
const dnsDomainRows = 50

// dnsSection is a device's "Looks up" section, on the page and as the
// fragment its period switch fetches.
type dnsSection struct {
	DeviceID int64
	Window   trafficWindow
	Windows  []trafficWindow

	// Read is whether any resolver's log has been read. Without one the
	// section is left off the page.
	Read bool

	Summary dnsSummary

	// Domains are the busiest registrable domains, each with the names under
	// it; Hidden counts the domains past them.
	Domains []*dnsDomain
	Hidden  int
}

// dnsSummary is the whole period at a glance.
type dnsSummary struct {
	Lookups, Names, Blocked, Failed int64
}

// dnsDomain is the names under one registrable domain, such as example.com
// for www.example.com and api.example.com, added up.
type dnsDomain struct {
	Domain                   string
	Lookups, Blocked, Failed int64
	LastHour                 time.Time
	Names                    []*inventory.DNSName
}

// buildDNSSection reads what device id looked up over the period windowKey
// names, ending at now.
func buildDNSSection(
	ctx context.Context, store *inventory.Store, id int64, windowKey string, now time.Time,
) (*dnsSection, error) {
	sec := &dnsSection{DeviceID: id, Window: windowFor(windowKey), Windows: trafficWindows}

	read, err := store.DNSRead(ctx)
	if err != nil || !read {
		return sec, err
	}

	sec.Read = true

	names, err := store.DeviceDNS(ctx, id, now.Add(-sec.Window.span))
	if err != nil {
		return nil, err
	}

	domains := make(map[string]*dnsDomain)

	for _, n := range names {
		sec.Summary.Lookups += n.Lookups
		sec.Summary.Blocked += n.Blocked
		sec.Summary.Failed += n.Failed

		key := registrableDomain(n.Name)

		d, ok := domains[key]
		if !ok {
			d = &dnsDomain{Domain: key}
			domains[key] = d
		}

		d.Lookups += n.Lookups
		d.Blocked += n.Blocked
		d.Failed += n.Failed

		if n.LastHour.After(d.LastHour) {
			d.LastHour = n.LastHour
		}

		d.Names = append(d.Names, n)
	}

	sec.Summary.Names = int64(len(names))

	all := slices.SortedFunc(maps.Values(domains), func(a, b *dnsDomain) int {
		return cmp.Or(cmp.Compare(b.Lookups, a.Lookups), cmp.Compare(a.Domain, b.Domain))
	})

	sec.Domains = all[:min(len(all), dnsDomainRows)]
	sec.Hidden = len(all) - len(sec.Domains)

	return sec, nil
}

// registrableDomain is the part of name a person registered, such as
// example.co.uk for www.example.co.uk. A name with none, such as a bare host
// name on the local network, stands for itself.
func registrableDomain(name string) string {
	d, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return name
	}

	return d
}

// deviceDNS serves the "Looks up" section alone, for its period switch.
func (h *Handler) deviceDNS() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		// A device that is not there is a 404 here too.
		if err := h.store.RequireDevice(r.Context(), id); err != nil {
			return err
		}

		data, err := buildDNSSection(r.Context(), h.store, id, r.URL.Query().Get("dns"), h.store.Now())
		if err != nil {
			return fmt.Errorf("device %d lookups: %w", id, err)
		}

		h.htmlWriter.Success(w, r, templatePartialDeviceDNS, data)

		return nil
	}
}
