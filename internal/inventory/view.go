package inventory

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// The types here are what the inventory gives to a caller that renders it. The
// generated models cannot do this. Every optional column reaches Go as a
// sql.NullString. It marshals as {"String":"","Valid":false}, and a template
// reads it as .Label.String. Here, an absent value is "". Every caller needs
// two facts: the name of a device, and whether it still answers. This code
// settles both once.

// Device is a device as it is displayed.
type Device struct {
	ID             int64                 `json:"id"`
	MAC            string                `json:"mac,omitempty"`
	IdentitySource dbtype.IdentitySource `json:"identity_source"`
	Randomised     bool                  `json:"randomised"`

	// Vendor is the organisation that registered the hardware address.
	// VendorShort is its short form in the OUI table, such as "ProxmoxServe".
	// Vendor is VendorShort until the next sighting of the device records the
	// registered name.
	Vendor      string `json:"vendor,omitempty"`
	VendorShort string `json:"vendor_short,omitempty"`

	Hostname       string                `json:"hostname,omitempty"`
	HostnameSource dbtype.HostnameSource `json:"hostname_source,omitempty"`
	Type           string                `json:"type,omitempty"`

	// Class is the device category that a surface shows as an icon. It is the
	// answer of the user in Type, if set. Else, it is the guess of the
	// classifier. ClassGuess is that guess, with or without the override.
	// ClassConfidence is its confidence. So the device page can show what the
	// classifier says alone.
	Class           classify.Class      `json:"class,omitempty"`
	ClassGuess      classify.Class      `json:"class_guess,omitempty"`
	ClassConfidence classify.Confidence `json:"class_confidence,omitempty"`

	Label   string `json:"label,omitempty"`
	Notes   string `json:"notes,omitempty"`
	Group   string `json:"group,omitempty"`
	Ignored bool   `json:"ignored"`

	// Watched tells whether the owner gets told when the device goes quiet or
	// comes back.
	Watched bool `json:"watched"`

	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`

	// Online reports whether the device was seen within the online window of
	// the store. It is fixed when the device is read. So a list does not
	// report two devices differently because of one second between them.
	Online bool `json:"online"`

	// PresentSince is when the current run of sightings of the device began.
	// It is zero while the device is quiet. It is also zero for a device that
	// no reading checked since the start of presence tracking.
	PresentSince time.Time `json:"present_since,omitzero"`

	// Current holds the addresses on which the device answers now, in address
	// order.
	Current []netip.Addr `json:"current_addresses,omitempty"`

	// Addresses is the full history, current entries first. Only Device fills
	// it. A list would need a row for each address to tell when each was seen.
	// A list shows only the current addresses.
	Addresses []*Address `json:"addresses,omitempty"`

	// Ports is every TCP port that a scan found open on the device, the ports
	// that still answer first. Only Device fills it, for the same reason as
	// Addresses.
	Ports []*Port `json:"ports,omitempty"`

	// OpenPorts is the numbers of the ports that a scan finds open now, in
	// number order. A list has these, because the full Ports history would be
	// too much for a row. The device page reads Ports.
	OpenPorts []uint16 `json:"open_ports,omitempty"`

	// Services is what the device advertised over DNS-SD in the latest sweep
	// that heard it, by type and instance. Only Device fills it, as for
	// Ports.
	Services []*Service `json:"services,omitempty"`

	// Networks is the set of recorded prefixes where the device has an address
	// now, in CIDR order. A list reads this to tell the network of a device.
	// The history of each address, and the prefixes of older addresses, are
	// on Addresses. Only the device page fills Addresses.
	Networks []*Network `json:"networks,omitempty"`
}

// Name is the name of the device. It is the label of the user. If there is
// none, it comes from the identity that the network gave.
func (d *Device) Name() string {
	var addr string
	if len(d.Current) > 0 {
		addr = d.Current[0].String()
	}

	return displayName(d.Label, d.Hostname, d.MAC, addr, d.ID)
}

// Address is one address that a device has, or had.
type Address struct {
	IP        netip.Addr `json:"ip"`
	Current   bool       `json:"current"`
	FirstSeen time.Time  `json:"first_seen"`
	LastSeen  time.Time  `json:"last_seen"`

	// Network is the recorded prefix where a sweep put this address. It is nil
	// when no sweep matched it to one. This is an address from before network
	// tracking, or an address on a prefix that nothing recorded.
	Network *Network `json:"network,omitzero"`
}

// Port is one TCP port on which a scan found a device listening. It can also be
// a port that was open once and closed later. The store records only a port
// seen open. So the list is what the device exposes now, plus what it exposed
// before.
type Port struct {
	Number  uint16           `json:"port"`
	Service string           `json:"service,omitempty"`
	State   dbtype.PortState `json:"state"`

	// FirstSeen is the first scan that found the port open. LastSeen is the
	// most recent scan that checked it. ChangedAt is the last change of the
	// state. So "open since" and "closed on" stay separate from both.
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	ChangedAt time.Time `json:"changed_at"`
}

// Service is one service that a device advertised over DNS-SD within the
// history window. The device announced it itself. So the list includes it,
// whether a port scan reached its port or not.
type Service struct {
	// Type is the service type without the domain, such as
	// "_googlecast._tcp".
	Type string `json:"type"`

	// Instance is the name that the device gives the service, such as "Living
	// Room TV". It is empty when that name was not safe to show.
	Instance string `json:"instance,omitempty"`

	// Port is where the service listens. It is zero when the device gave
	// none.
	Port uint16 `json:"port,omitempty"`

	// Label is the name that a Google Cast device shows its owner. Model is
	// its model, such as "Google Nest Mini". Both are empty for any other
	// service type.
	Label string `json:"label,omitempty"`
	Model string `json:"model,omitempty"`

	// FirstSeen is the first sweep that heard the service. LastSeen is the
	// latest.
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// PortOverview is the short service summary on the overview page.
//
// Open is how many ports are open now, across Devices devices. Opened and
// Closed count the ports that changed state within PortChangeWindow.
type PortOverview struct {
	Open     int             `json:"open"`
	Devices  int             `json:"devices"`
	Opened   int             `json:"opened"`
	Closed   int             `json:"closed"`
	Services []*ServiceCount `json:"services"`
}

// ServiceCount reports how many devices answer now on one known service. If the
// static map has no name for it, it counts one port number.
type ServiceCount struct {
	Port    uint16 `json:"port"`
	Service string `json:"service,omitempty"`
	Devices int    `json:"devices"`
}

// Name is the known service name. If the static service map of the scanner
// does not know one, it is the port.
func (s *ServiceCount) Name() string {
	if s.Service != "" {
		return s.Service
	}

	return "Port " + strconv.Itoa(int(s.Port))
}

// Open reports whether the port answered the most recent scan that reached
// it.
func (p *Port) Open() bool { return p.State == dbtype.PortOpen }

// Claim is what one source says about a device, as it is displayed.
//
// The device row has one name. This is every name that a source gave for it.
// So a source that lost the election is still readable.
type Claim struct {
	Source string            `json:"source"`
	Kind   dbtype.SourceKind `json:"kind"`

	// Address is the address of this claim. A source makes one claim for each
	// address at which it reports the device.
	Address netip.Addr `json:"address"`

	Hostname       string                `json:"hostname,omitempty"`
	HostnameSource dbtype.HostnameSource `json:"hostname_source,omitempty"`

	// Detail is what only this source knows, decoded from its stored JSON. It
	// is sorted by key. So a page renders it in the same way twice.
	Detail []Field `json:"detail,omitempty"`

	// FirstSeen and LastSeen are the sighting of this source. It can be
	// different from the sighting of the device. A router can have a bound
	// lease for a device that did not answer a ping for days. Then two sources
	// disagree. These fields exist to show this.
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// Field is one entry of the detail of a claim.
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

	// DeviceName names the device of the event. It is empty when the device
	// was deleted, and when the caller already knows the device.
	DeviceName string `json:"device_name,omitempty"`
}

// Scan is one run of one source.
type Scan struct {
	ID      int64             `json:"id"`
	Source  string            `json:"source"`
	Kind    dbtype.ScanKind   `json:"kind"`
	Scope   dbtype.ScanScope  `json:"scope"`
	Network string            `json:"network,omitempty"`
	Status  dbtype.ScanStatus `json:"status"`
	Error   string            `json:"error,omitempty"`
	Found   int               `json:"found"`

	StartedAt time.Time `json:"started_at"`

	// FinishedAt is zero while the scan still runs.
	FinishedAt time.Time `json:"finished_at,omitzero"`
}

// Took is how long the scan ran. It is zero while the scan still runs.
//
// It comes from the two timestamps, which already tell it. A time.Duration has
// no JSON form. encoding/json/v2 refuses to marshal one.
func (s *Scan) Took() time.Duration {
	if s.FinishedAt.IsZero() {
		return 0
	}

	return s.FinishedAt.Sub(s.StartedAt)
}

// Stats counts the whole inventory. Every count includes all devices, also the
// ignored devices. Ignored reports them separately.
type Stats struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
	Ignored int `json:"ignored"`

	// Discovered counts devices first seen within DiscoveryWindow.
	Discovered int `json:"discovered"`
}

// Network is one prefix that the inventory recorded, as it is displayed. The
// counts are of devices with a current address on it. They use the same online
// window as a device list.
type Network struct {
	ID   int64  `json:"id"`
	CIDR string `json:"cidr"`
	Name string `json:"name,omitempty"`

	// VLAN is the tag of the network. It is zero when the network has none.
	// The schema allows a network without a tag. An untagged network is a real
	// answer.
	VLAN int `json:"vlan,omitempty"`

	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
}

// Status filters a device list by whether the devices still answer.
type Status string

// Status values for the online/offline state of a filter.
const (
	StatusAny     Status = ""
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
)

var statuses = []Status{StatusAny, StatusOnline, StatusOffline}

// Valid reports whether s is a status that filters. A caller that rejects an
// invalid status tells the user that the filter was misspelt. A caller that
// does not still gets the whole list.
func (s Status) Valid() bool { return slices.Contains(statuses, s) }

// admits reports whether a device in the given state passes the filter. An
// unknown status filters nothing. So a hand-typed query parameter makes the
// list wider.
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

// Sort orders a device list. The order happens in Go, because addresses are
// stored as TEXT. TEXT sorts 192.0.2.9 after 192.0.2.100. Also, a name comes
// from several columns.
type Sort string

// Sort names an ordering for a device list.
const (
	// SortDefault is SortLastSeen. So an unset field needs no special case.
	SortDefault Sort = ""

	// SortLastSeen puts the most recently seen device first.
	SortLastSeen Sort = "last_seen"

	SortName    Sort = "name"
	SortAddress Sort = "address"

	// SortType groups the list by device class. The classified devices come
	// first, and the unclassified devices after them.
	SortType Sort = "type"
)

var sorts = []Sort{SortDefault, SortLastSeen, SortName, SortAddress, SortType}

// Valid reports whether by names an ordering.
func (by Sort) Valid() bool { return slices.Contains(sorts, by) }

// DeviceFilter limits a device list.
type DeviceFilter struct {
	// Query matches a substring of the label, hostname, vendor, hardware
	// address, or any current address.
	Query string
	Group string

	// Network allows only devices with a current address on the network with
	// this id. Zero is every network.
	Network int64

	// Type allows only devices with this effective class. The effective class
	// is the override of the user, if set. Else, it is the guess of the
	// classifier. The zero class is every type. It matches the same field as
	// the icon column. So the filter selects what the list already shows.
	Type classify.Class

	Status Status
	Sort   Sort

	// Watched allows only the devices that the owner watches. False is every
	// device.
	Watched bool

	// IncludeIgnored allows the devices that the user ignored. Else, the list
	// leaves them out.
	IncludeIgnored bool
}

// newDevice converts a stored device. It decides whether the device counts as
// online against a single cutoff. So every device in one read uses the same
// rule.
func newDevice(d *models.Device, cutoff time.Time) *Device {
	guess := classify.Class(d.DeviceClass.String)

	// The answer of the user wins when it names a class that this build
	// knows. A value that does not, such as an old free-text type, is no
	// override. Then the guess stays.
	effective := guess
	if override := classify.Class(d.DeviceType.String); override.Known() {
		effective = override
	}

	return &Device{
		ID:              d.ID,
		MAC:             macString(d.MAC),
		IdentitySource:  d.IdentitySource,
		Randomised:      d.IsRandomised,
		Vendor:          cmp.Or(d.Vendor.String, d.VendorShort.String),
		VendorShort:     d.VendorShort.String,
		Hostname:        d.Hostname.String,
		HostnameSource:  d.HostnameSource,
		Type:            d.DeviceType.String,
		Class:           effective,
		ClassGuess:      guess,
		ClassConfidence: classify.Confidence(d.DeviceClassConfidence.String),
		Label:           d.Label.String,
		Notes:           d.Notes.String,
		Group:           d.GroupName.String,
		Ignored:         d.IsIgnored,
		Watched:         d.IsWatched,
		FirstSeen:       d.FirstSeen.Time,
		LastSeen:        d.LastSeen.Time,
		Online:          !d.LastSeen.Before(cutoff),
		PresentSince:    d.PresentSince.Time.Time,
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

func newPort(p *models.DevicePort) *Port {
	return &Port{
		// A CHECK limits device_ports.port to 1-65535, so it fits.
		Number:    uint16(p.Port), //nolint:gosec // range enforced by the column CHECK.
		Service:   p.Service.String,
		State:     p.State,
		FirstSeen: p.FirstSeen.Time,
		LastSeen:  p.LastSeen.Time,
		ChangedAt: p.ChangedAt.Time,
	}
}

func newService(r *models.DeviceService) *Service {
	return &Service{
		Type:      r.Type,
		Instance:  r.Instance,
		Port:      uint16(r.Port), //nolint:gosec // range enforced by the column CHECK.
		Label:     r.Label.String,
		Model:     r.Model.String,
		FirstSeen: r.FirstSeen.Time,
		LastSeen:  r.LastSeen.Time,
	}
}

func newClaim(r *models.ListDeviceSourcesRow) *Claim {
	return &Claim{
		Source:         r.SourceName,
		Kind:           r.SourceKind,
		Address:        r.DeviceSource.Address.Addr,
		Hostname:       r.DeviceSource.Hostname.String,
		HostnameSource: r.DeviceSource.HostnameSource,
		Detail:         claimFields(r.DeviceSource.Detail.String),
		FirstSeen:      r.DeviceSource.FirstSeen.Time,
		LastSeen:       r.DeviceSource.LastSeen.Time,
	}
}

// claimFields decodes the stored detail of a claim.
//
// It drops detail that does not decode. The detail is an extra note of a
// source about one device. A page that does not render because of it would
// hide everything else about the device.
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

// newScan adds source and network names to a recorded scan. FinishedAt is zero
// until the scan finished.
func newScan(s *models.Scan, source, network string) *Scan {
	sc := &Scan{
		ID:        s.ID,
		Source:    source,
		Kind:      s.Kind,
		Scope:     s.Scope,
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

// displayName picks the first identifying value that is set. If none is, it
// uses the id of the row. So a device with nothing known still has a name.
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

// parsePorts reads the port numbers that GROUP_CONCAT put into one column, and
// sorts them. The aggregate has no useful order. It drops a value that is not a
// port. The column comes from a column with a CHECK. So a value that does not
// parse is a row written outside the application.
func parsePorts(concat string) []uint16 {
	if concat == "" {
		return nil
	}

	fields := strings.Fields(concat)
	ports := make([]uint16, 0, len(fields))

	for _, f := range fields {
		if n, err := strconv.ParseUint(f, 10, 16); err == nil && n > 0 {
			ports = append(ports, uint16(n))
		}
	}

	slices.Sort(ports)

	return ports
}

// parseIDs reads the row ids that GROUP_CONCAT put into one column. It does not
// keep an order. The caller resolves these against a list with its own
// order.
func parseIDs(concat string) []int64 {
	if concat == "" {
		return nil
	}

	parts := strings.Split(concat, ",")
	ids := make([]int64, 0, len(parts))

	for _, p := range parts {
		if id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}

	return ids
}

// parseAddrs reads the addresses that GROUP_CONCAT put into one column, and
// sorts them. Neither the concatenation nor their TEXT column has a useful
// order.
func parseAddrs(concat string) []netip.Addr {
	if concat == "" {
		return nil
	}

	fields := strings.Fields(concat)
	addrs := make([]netip.Addr, 0, len(fields))

	for _, f := range fields {
		// Every address reached the column through dbtype.Addr. So an address
		// that does not parse is a row written outside the application.
		if a, err := netip.ParseAddr(f); err == nil {
			addrs = append(addrs, a)
		}
	}

	slices.SortFunc(addrs, netip.Addr.Compare)

	return addrs
}

// sortDevices sorts the list in place. A tie uses the id. So a repeated read
// returns the same order.
func sortDevices(devices []*Device, by Sort) {
	var cmpFn func(a, b *Device) int

	switch by {
	case SortName:
		cmpFn = func(a, b *Device) int {
			return cmp.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name()))
		}
	case SortAddress:
		cmpFn = compareFirstAddr
	case SortType:
		cmpFn = compareClass
	default:
		// SortLastSeen, and the unset field that means the same.
		cmpFn = func(a, b *Device) int { return b.LastSeen.Compare(a.LastSeen) }
	}

	slices.SortStableFunc(devices, func(a, b *Device) int {
		return cmp.Or(cmpFn(a, b), cmp.Compare(a.ID, b.ID))
	})
}

// compareClass orders by device class, and keeps the same classes together. An
// unclassified device sorts last. It has no class for a group. A reader who
// sorts by type looks for the devices that have one.
func compareClass(a, b *Device) int {
	switch {
	case a.Class == b.Class:
		return 0
	case a.Class == classify.Unknown:
		return 1
	case b.Class == classify.Unknown:
		return -1
	}

	return cmp.Compare(a.Class, b.Class)
}

// compareFirstAddr orders by the lowest current address of a device. A device
// with no address sorts last, because it has nothing to compare.
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
