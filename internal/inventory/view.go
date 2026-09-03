package inventory

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// The types here are what the inventory hands to a caller that renders it. The
// generated models cannot serve that purpose: every optional column reaches Go
// as a sql.NullString, which marshals as {"String":"","Valid":false} and reads
// as .Label.String in a template. An absent value is "" here instead, and the
// two facts every caller derives -- what to call a device and whether it is
// still answering -- are settled once, here, rather than in each surface.

// Device is a device as it is displayed.
type Device struct {
	ID             int64                 `json:"id"`
	MAC            string                `json:"mac,omitempty"`
	IdentitySource dbtype.IdentitySource `json:"identity_source"`
	Randomised     bool                  `json:"randomised"`
	Vendor         string                `json:"vendor,omitempty"`
	Hostname       string                `json:"hostname,omitempty"`
	HostnameSource dbtype.HostnameSource `json:"hostname_source,omitempty"`
	Type           string                `json:"type,omitempty"`

	Label   string `json:"label,omitempty"`
	Notes   string `json:"notes,omitempty"`
	Group   string `json:"group,omitempty"`
	Ignored bool   `json:"ignored"`

	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`

	// Online reports whether the device was seen inside the store's online
	// window. It is fixed when the device is read, so a list does not report
	// two devices differently for having been formatted a second apart.
	Online bool `json:"online"`

	// Current holds the addresses the device answers on now, in address order.
	Current []netip.Addr `json:"current_addresses,omitempty"`

	// Addresses is the full history, current entries first. Only Device
	// fills it: a list would need a row per address to say when each was seen,
	// and it only ever shows the current ones.
	Addresses []*Address `json:"addresses,omitempty"`
}

// Name is what to call the device: whatever the user labelled it, falling back
// through the identity the network offered.
func (d *Device) Name() string {
	var addr string
	if len(d.Current) > 0 {
		addr = d.Current[0].String()
	}

	return displayName(d.Label, d.Hostname, d.MAC, addr, d.ID)
}

// Address is one address a device holds, or held.
type Address struct {
	IP        netip.Addr `json:"ip"`
	Current   bool       `json:"current"`
	FirstSeen time.Time  `json:"first_seen"`
	LastSeen  time.Time  `json:"last_seen"`

	// Network is the recorded prefix a sweep placed this address on. It is nil
	// when no sweep matched it to one -- an address older than network
	// tracking, or one on a prefix nothing has recorded.
	Network *Network `json:"network,omitempty"`
}

// Claim is what one source says about a device, as it is displayed.
//
// The device row carries one name; this is every name that was offered for it,
// so a source that lost the election is still readable rather than discarded.
type Claim struct {
	Source string            `json:"source"`
	Kind   dbtype.SourceKind `json:"kind"`

	Hostname       string                `json:"hostname,omitempty"`
	HostnameSource dbtype.HostnameSource `json:"hostname_source,omitempty"`

	// Detail is what only this source knows, decoded from the JSON it was
	// stored as and sorted by key so a page renders it the same way twice.
	Detail []Field `json:"detail,omitempty"`

	// FirstSeen and LastSeen are this source's own sighting, not the device's.
	// A router holding a bound lease for something that has not answered a ping
	// in days is two sources disagreeing, which is what these are here to show.
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// Field is one entry of a claim's detail.
type Field struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Event is one entry of the change log.
type Event struct {
	ID       int64            `json:"id"`
	DeviceID int64            `json:"device_id,omitempty"`
	ScanID   int64            `json:"scan_id,omitempty"`
	Kind     dbtype.EventKind `json:"kind"`
	OldValue string           `json:"old_value,omitempty"`
	NewValue string           `json:"new_value,omitempty"`
	Detail   string           `json:"detail,omitempty"`
	At       time.Time        `json:"occurred_at"`

	// DeviceName names the device the event was about. It is empty when the
	// device has since been deleted, and when the caller already knows which
	// device it asked about.
	DeviceName string `json:"device_name,omitempty"`
}

// Scan is one run of one source.
type Scan struct {
	ID      int64             `json:"id"`
	Source  string            `json:"source"`
	Kind    dbtype.ScanKind   `json:"kind"`
	Network string            `json:"network,omitempty"`
	Status  dbtype.ScanStatus `json:"status"`
	Error   string            `json:"error,omitempty"`
	Found   int               `json:"found"`

	StartedAt time.Time `json:"started_at"`

	// FinishedAt is zero while the scan is still running.
	FinishedAt time.Time `json:"finished_at,omitzero"`
}

// Took is how long the scan ran, and is zero while it still is.
//
// It is derived rather than stored: the two timestamps already say it, and a
// time.Duration has no JSON representation to be sent as -- encoding/json/v2
// refuses to marshal one at all.
func (s *Scan) Took() time.Duration {
	if s.FinishedAt.IsZero() {
		return 0
	}

	return s.FinishedAt.Sub(s.StartedAt)
}

// Stats counts the inventory as a whole. Every count is over all devices,
// including the ignored ones, which Ignored reports separately.
type Stats struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
	Ignored int `json:"ignored"`

	// Discovered counts devices first seen within DiscoveryWindow.
	Discovered int `json:"discovered"`
}

// Network is one prefix the inventory has recorded, as it is displayed. The
// counts are of devices holding a current address on it, judged by the same
// online window a device list is.
type Network struct {
	ID   int64  `json:"id"`
	CIDR string `json:"cidr"`
	Name string `json:"name,omitempty"`

	// VLAN is the tag the network carries, and is zero when it carries none.
	// The schema allows a network without one, and an untagged network is a
	// real thing rather than a missing value.
	VLAN int `json:"vlan,omitempty"`

	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
}

// Status filters a device list by whether the devices are still answering.
type Status string

// Status values a filter's online/offline state can take.
const (
	StatusAny     Status = ""
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
)

var statuses = []Status{StatusAny, StatusOnline, StatusOffline}

// Valid reports whether s is one of the statuses that filters anything. A
// caller that rejects an invalid one tells the user their filter was misspelt;
// one that does not still gets the whole list back rather than none of it.
func (s Status) Valid() bool { return slices.Contains(statuses, s) }

// admits reports whether a device in the given state passes the filter. An
// unrecognised status filters nothing out, so a hand-typed query parameter
// widens the list rather than emptying it.
func (s Status) admits(online bool) bool {
	switch s {
	case StatusOnline:
		return online
	case StatusOffline:
		return !online
	default:
		return true
	}
}

// Sort orders a device list. The ordering is applied in Go rather than SQL
// because addresses are stored as TEXT, which sorts 192.0.2.9 after
// 192.0.2.100, and because a name is assembled from several columns.
type Sort string

// Sort names an ordering for a device list.
const (
	// SortDefault is SortLastSeen, so an unset field needs no special case.
	SortDefault Sort = ""

	// SortLastSeen puts the most recently seen device first.
	SortLastSeen Sort = "last_seen"

	SortName    Sort = "name"
	SortAddress Sort = "address"
)

var sorts = []Sort{SortDefault, SortLastSeen, SortName, SortAddress}

// Valid reports whether by names an ordering.
func (by Sort) Valid() bool { return slices.Contains(sorts, by) }

// DeviceFilter narrows a device list.
type DeviceFilter struct {
	// Query matches a substring of the label, hostname, vendor, hardware
	// address, or any current address.
	Query string
	Group string

	// Network admits only devices holding a current address on the network
	// with this id. Zero is every network.
	Network int64

	Status Status
	Sort   Sort

	// IncludeIgnored admits the devices the user has marked ignored, which are
	// left out otherwise.
	IncludeIgnored bool
}

// newDevice converts a stored device, resolving whether it counts as online
// against a single cutoff so every device in one read is judged alike.
func newDevice(d *models.Device, cutoff time.Time) *Device {
	return &Device{
		ID:             d.ID,
		MAC:            macString(d.MAC),
		IdentitySource: d.IdentitySource,
		Randomised:     d.IsRandomised,
		Vendor:         d.Vendor.String,
		Hostname:       d.Hostname.String,
		HostnameSource: d.HostnameSource,
		Type:           d.DeviceType.String,
		Label:          d.Label.String,
		Notes:          d.Notes.String,
		Group:          d.GroupName.String,
		Ignored:        d.IsIgnored,
		FirstSeen:      d.FirstSeen.Time,
		LastSeen:       d.LastSeen.Time,
		Online:         !d.LastSeen.Before(cutoff),
	}
}

func newAddress(a *models.Address) *Address {
	return &Address{
		IP:        a.IP.Addr,
		Current:   a.IsCurrent,
		FirstSeen: a.FirstSeen.Time,
		LastSeen:  a.LastSeen.Time,
	}
}

func newClaim(r *models.ListDeviceSourcesRow) *Claim {
	return &Claim{
		Source:         r.SourceName,
		Kind:           r.SourceKind,
		Hostname:       r.DeviceSource.Hostname.String,
		HostnameSource: r.DeviceSource.HostnameSource,
		Detail:         claimFields(r.DeviceSource.Detail.String),
		FirstSeen:      r.DeviceSource.FirstSeen.Time,
		LastSeen:       r.DeviceSource.LastSeen.Time,
	}
}

// claimFields decodes a claim's stored detail.
//
// Detail that will not decode is dropped rather than reported: it is a source's
// aside about one device, and a page that refuses to render because of it would
// hide everything else the device knows.
func claimFields(raw string) []Field {
	if raw == "" {
		return nil
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}

	fields := make([]Field, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		fields = append(fields, Field{Key: k, Value: m[k]})
	}

	return fields
}

func newEvent(e *models.Event) *Event {
	return &Event{
		ID:       e.ID,
		DeviceID: e.DeviceID.Int64,
		ScanID:   e.ScanID.Int64,
		Kind:     e.Kind,
		OldValue: e.OldValue.String,
		NewValue: e.NewValue.String,
		Detail:   e.Detail.String,
		At:       e.OccurredAt.Time,
	}
}

func newScan(s *models.Scan, source, network string) *Scan {
	sc := &Scan{
		ID:        s.ID,
		Source:    source,
		Kind:      s.Kind,
		Network:   network,
		Status:    s.Status,
		Error:     s.Error.String,
		Found:     int(s.FoundCount),
		StartedAt: s.StartedAt.Time,
	}

	if s.FinishedAt.Valid {
		sc.FinishedAt = s.FinishedAt.Time.Time
	}

	return sc
}

// displayName picks the first identifying value that is set, falling back to
// the row's own id so a device with nothing known about it is still nameable.
func displayName(label, hostname, mac, addr string, id int64) string {
	if n := cmp.Or(label, hostname, mac, addr); n != "" {
		return n
	}

	if id == 0 {
		return ""
	}

	return fmt.Sprintf("device %d", id)
}

func macString(m dbtype.MAC) string {
	if !m.Valid() {
		return ""
	}

	return m.String()
}

// parseAddrs reads the addresses GROUP_CONCAT packed into one column and orders
// them, since neither the concatenation nor the TEXT column they came from has
// an order worth keeping.
func parseAddrs(concat string) []netip.Addr {
	if concat == "" {
		return nil
	}

	fields := strings.Fields(concat)
	addrs := make([]netip.Addr, 0, len(fields))

	for _, f := range fields {
		// Every address reached the column through dbtype.Addr, so one that
		// will not parse back is a row written around the application.
		if a, err := netip.ParseAddr(f); err == nil {
			addrs = append(addrs, a)
		}
	}

	slices.SortFunc(addrs, func(a, b netip.Addr) int { return a.Compare(b) })

	return addrs
}

// sortDevices orders the list in place. Ties fall back to the id so that a
// repeated read returns the same order rather than SQLite's.
func sortDevices(devices []*Device, by Sort) {
	var cmpFn func(a, b *Device) int

	switch by {
	case SortName:
		cmpFn = func(a, b *Device) int {
			return cmp.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name()))
		}
	case SortAddress:
		cmpFn = compareFirstAddr
	default:
		// SortLastSeen, and the unset field that means it.
		cmpFn = func(a, b *Device) int { return b.LastSeen.Compare(a.LastSeen) }
	}

	slices.SortStableFunc(devices, func(a, b *Device) int {
		return cmp.Or(cmpFn(a, b), cmp.Compare(a.ID, b.ID))
	})
}

// compareFirstAddr orders by the lowest address a device currently holds. A
// device holding none sorts last: it has nothing to compare, not the lowest
// address there is.
func compareFirstAddr(a, b *Device) int {
	switch {
	case len(a.Current) == 0 && len(b.Current) == 0:
		return 0
	case len(a.Current) == 0:
		return 1
	case len(b.Current) == 0:
		return -1
	}

	return a.Current[0].Compare(b.Current[0])
}
