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

const (
	// dnsDomainRows is how many domains the section lists. The summary and a
	// line under the table count the rest.
	dnsDomainRows = 50

	// dnsDomainNames is how many names an opened domain lists. The domain of a
	// CDN can hold hundreds. A line under them counts the rest.
	dnsDomainNames = 20
)

// dnsSection is the "Looks up" section of a device, on the page and as the
// fragment that its period switch fetches.
type dnsSection struct {
	DeviceID int64
	Window   trafficWindow
	Windows  []trafficWindow

	// Read tells whether Jocasta read the log of any resolver. Without one,
	// the page leaves out the section.
	Read bool

	// Summary adds up every name in the period, not only the listed names.
	Summary *inventory.DNSTotals

	// Domains are the busiest registrable domains, each with its names.
	// Hidden counts the domains after them.
	Domains []*dnsDomain
	Hidden  int64
}

// dnsDomain is the total of the names under one registrable domain, such as
// example.com for www.example.com and api.example.com.
type dnsDomain struct {
	Domain                   string
	Lookups, Blocked, Failed int64
	LastHour                 time.Time

	// Names are the busiest names under the domain. Hidden counts the names
	// after them. NameCount counts all of them.
	Names     []*inventory.DNSName
	Hidden    int64
	NameCount int64
}

// buildDNSSection reads what device id looked up over the period that windowKey
// names, until now.
func buildDNSSection(
	ctx context.Context, store *inventory.Store, id int64, windowKey string, now time.Time,
) (*dnsSection, error) {
	sec := &dnsSection{DeviceID: id, Window: windowFor(windowKey), Windows: trafficWindows}

	read, err := store.DNSRead(ctx)
	if err != nil || !read {
		return sec, err
	}

	sec.Read = true

	since := now.Add(-sec.Window.span)

	if sec.Summary, err = store.DeviceDNSTotals(ctx, id, since); err != nil {
		return nil, err
	}

	names, err := store.DeviceDNS(ctx, id, since)
	if err != nil {
		return nil, err
	}

	domains := make(map[string]*dnsDomain)

	for _, n := range names {
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

	all := slices.SortedFunc(maps.Values(domains), func(a, b *dnsDomain) int {
		return cmp.Or(cmp.Compare(b.Lookups, a.Lookups), cmp.Compare(a.Domain, b.Domain))
	})

	sec.Domains = all[:min(len(all), dnsDomainRows)]
	sec.Hidden = int64(len(all) - len(sec.Domains))

	for _, d := range sec.Domains {
		d.NameCount = int64(len(d.Names))
		d.Names = d.Names[:min(len(d.Names), dnsDomainNames)]
		d.Hidden = d.NameCount - int64(len(d.Names))
	}

	return sec, nil
}

// registrableDomain is the part of name that a person registered, such as
// example.co.uk for www.example.co.uk. A name without one, such as a bare host
// name on the local network, is its own domain.
func registrableDomain(name string) string {
	d, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return name
	}

	return d
}

// deviceDNS serves only the "Looks up" section, for its period switch.
func (h *Handler) deviceDNS() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		// A missing device is also a 404 here.
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
