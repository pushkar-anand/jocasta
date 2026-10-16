package dbtype

import (
	"database/sql/driver"
	"fmt"
	"slices"
)

// SourceKind is the kind of thing that produced a fact, for any
// implementation. For example, RouterOS is one of several routers that this
// could talk to.
type SourceKind string

// SourceKind values for the origin of a fact.
const (
	SourceSweep  SourceKind = "SWEEP"
	SourceRouter SourceKind = "ROUTER"
	SourceDNS    SourceKind = "DNS"
	SourceManual SourceKind = "MANUAL"
)

var sourceKinds = []SourceKind{SourceSweep, SourceRouter, SourceDNS, SourceManual}

// Valid reports whether k is one of the known source kinds.
func (k SourceKind) Valid() bool { return slices.Contains(sourceKinds, k) }

// Value renders k for the driver. It refuses a value that the column does not
// allow.
func (k SourceKind) Value() (driver.Value, error) { return enumValue(k, sourceKinds, "source kind") }

// Scan reads a stored source kind into k.
func (k *SourceKind) Scan(src any) error { return enumScan(k, sourceKinds, "source kind", src) }

// IdentitySource is the key of a device row. A device seen only over IP is
// provisional. Only the address that it answered on identifies it. So it can
// later merge into a device with a hardware address.
type IdentitySource string

// IdentitySource values for the identity of a device row.
const (
	IdentityMAC IdentitySource = "MAC"
	IdentityIP  IdentitySource = "IP"
)

var identitySources = []IdentitySource{IdentityMAC, IdentityIP}

// Valid reports whether s is one of the known identity sources.
func (s IdentitySource) Valid() bool { return slices.Contains(identitySources, s) }

// Value renders s for the driver. It refuses a value that the column does not
// allow.
func (s IdentitySource) Value() (driver.Value, error) {
	return enumValue(s, identitySources, "identity source")
}

// Scan reads a stored identity source into s.
func (s *IdentitySource) Scan(src any) error {
	return enumScan(s, identitySources, "identity source", src)
}

// ScanKind is the purpose of a scan.
type ScanKind string

// ScanKind values for the purpose of a scan.
const (
	ScanDiscovery ScanKind = "DISCOVERY"
	ScanPorts     ScanKind = "PORTS"
)

var scanKinds = []ScanKind{ScanDiscovery, ScanPorts}

// ScanKinds returns every known scan kind. It is a copy. So a caller cannot
// change what Valid allows.
func ScanKinds() []ScanKind { return slices.Clone(scanKinds) }

// Valid reports whether k is one of the known scan kinds.
func (k ScanKind) Valid() bool { return slices.Contains(scanKinds, k) }

// Value renders k for the driver. It refuses a value that the column does not
// allow.
func (k ScanKind) Value() (driver.Value, error) { return enumValue(k, scanKinds, "scan kind") }

// Scan reads a stored scan kind into k.
func (k *ScanKind) Scan(src any) error { return enumScan(k, scanKinds, "scan kind", src) }

// ScanStatus is how a scan ended, or that it did not end yet. A row that stays
// running is a scan whose process stopped before it could record the end.
type ScanStatus string

// ScanStatus values for the outcome of a scan.
const (
	StatusRunning   ScanStatus = "RUNNING"
	StatusOK        ScanStatus = "OK"
	StatusFailed    ScanStatus = "FAILED"
	StatusCancelled ScanStatus = "CANCELLED"
)

var scanStatuses = []ScanStatus{StatusRunning, StatusOK, StatusFailed, StatusCancelled}

// Valid reports whether s is one of the known scan statuses.
func (s ScanStatus) Valid() bool { return slices.Contains(scanStatuses, s) }

// Value renders s for the driver. It refuses a value that the column does not
// allow.
func (s ScanStatus) Value() (driver.Value, error) { return enumValue(s, scanStatuses, "scan status") }

// Scan reads a stored scan status into s.
func (s *ScanStatus) Scan(src any) error { return enumScan(s, scanStatuses, "scan status", src) }

// ScanScope is what a scan covered: every target that its kind reads, or only
// the devices that a sweep just found.
type ScanScope string

// ScanScope values for the coverage of a scan.
const (
	ScopeFull       ScanScope = "FULL"
	ScopeNewDevices ScanScope = "NEW_DEVICES"
)

var scanScopes = []ScanScope{ScopeFull, ScopeNewDevices}

// Value renders s for the driver. It refuses a value that the column does not
// allow.
func (s ScanScope) Value() (driver.Value, error) { return enumValue(s, scanScopes, "scan scope") }

// Scan reads a stored scan scope into s.
func (s *ScanScope) Scan(src any) error { return enumScan(s, scanScopes, "scan scope", src) }

// PortState is what a probe last found at a port. If a port answered once, it
// keeps its row when it stops answering, and its state changes to closed. So
// the record of what a device exposed stays after the service stops.
type PortState string

// PortState values for a probed port.
const (
	PortOpen   PortState = "open"
	PortClosed PortState = "closed"
)

var portStates = []PortState{PortOpen, PortClosed}

// Valid reports whether s is one of the known port states.
func (s PortState) Valid() bool { return slices.Contains(portStates, s) }

// Value renders s for the driver. It refuses a value that the column does not
// allow.
func (s PortState) Value() (driver.Value, error) { return enumValue(s, portStates, "port state") }

// Scan reads a stored port state into s.
func (s *PortState) Scan(src any) error { return enumScan(s, portStates, "port state", src) }

// EventKind is what changed. The column has no CHECK, so a new kind needs no
// migration. So only this list keeps a typo out of the permanent record.
type EventKind string

// EventKind values for a logged change.
const (
	EventDeviceDiscovered EventKind = "DEVICE_DISCOVERED"
	EventDeviceIdentified EventKind = "DEVICE_IDENTIFIED"
	EventDevicesMerged    EventKind = "DEVICES_MERGED"
	EventAddressAdded     EventKind = "ADDRESS_ADDED"
	EventHostnameChanged  EventKind = "HOSTNAME_CHANGED"

	// EventAddressReleased records that a sweep found that a device left an
	// address. The device answered on a different address in the prefix. This
	// address did not answer for longer than the grace window.
	EventAddressReleased EventKind = "ADDRESS_RELEASED"

	// EventDeviceEdited records that the user changed their own fields on a
	// device. Their edits go in the change log, as the edits of a scan do.
	// The log records every change, from any source.
	EventDeviceEdited EventKind = "DEVICE_EDITED"

	// EventPortOpened records that a port scan found a device that listens on
	// a new port. EventPortClosed records that a port stopped answering.
	EventPortOpened EventKind = "PORT_OPENED"
	EventPortClosed EventKind = "PORT_CLOSED"

	// EventDeviceClassified records that the classifier of a scan changed its
	// guess from one known class to another. The first guess logs no event,
	// because it is part of the discovery of the device. A guess that changes
	// back to nothing also logs no event. The answer of the user, in
	// device_type, logs as EventDeviceEdited.
	EventDeviceClassified EventKind = "DEVICE_CLASSIFIED"

	// EventDeviceQuiet and EventDeviceBack record that a watched device
	// crossed the online window. For EventDeviceQuiet, a reading that could
	// see the device found it gone for longer than the window. For
	// EventDeviceBack, a reading saw it again after that. Devices that nobody
	// watches log no event, because phones and laptops do this all day.
	EventDeviceQuiet EventKind = "DEVICE_QUIET"
	EventDeviceBack  EventKind = "DEVICE_BACK"
)

// eventKinds starts with a watched device that goes quiet. Of all changes that
// a scan can report, an owner asked first to hear about this one.
var eventKinds = []EventKind{
	EventDeviceQuiet,
	EventDeviceBack,
	EventDeviceDiscovered,
	EventDeviceIdentified,
	EventDevicesMerged,
	EventAddressAdded,
	EventHostnameChanged,
	EventAddressReleased,
	EventDeviceEdited,
	EventPortOpened,
	EventPortClosed,
	EventDeviceClassified,
}

// Valid reports whether k is one of the known event kinds.
func (k EventKind) Valid() bool { return slices.Contains(eventKinds, k) }

// EventKinds returns every known event kind, in the order of the log filters.
// It is a copy. So a caller cannot change what Valid allows.
func EventKinds() []EventKind { return slices.Clone(eventKinds) }

// Value renders k for the driver. It refuses a value that the column does not
// allow.
func (k EventKind) Value() (driver.Value, error) { return enumValue(k, eventKinds, "event kind") }

// Scan reads a stored event kind into k.
func (k *EventKind) Scan(src any) error { return enumScan(k, eventKinds, "event kind", src) }

// HostnameSource names the origin of the name of a device. A sweep resolves
// names in one way. Other sources bring their own names, and the inventory
// must keep them separate. Its zero value is null, as for MAC, for a device
// with no name yet.
type HostnameSource string

// HostnameSource values for the origin of the name of a device.
const (
	// HostnameFromDNS is a name that a sweep got from a lookup of the
	// address.
	HostnameFromDNS HostnameSource = "DNS"

	// HostnameFromDHCPStatic and HostnameFromDHCPLease are both the claim of
	// the client about itself. Their ranks are different, because an operator
	// bound the static lease on purpose. So the operator already checked its
	// name.
	HostnameFromDHCPStatic HostnameSource = "DHCP_STATIC"
	HostnameFromDHCPLease  HostnameSource = "DHCP_LEASE"

	// HostnameFromMDNS is a name that a sweep got from the device over mDNS.
	// The device answers to this name on the local network.
	HostnameFromMDNS HostnameSource = "MDNS"

	// HostnameFromNetBIOS is a name that a sweep got from the device over
	// NetBIOS. Windows and Samba answer NetBIOS.
	HostnameFromNetBIOS HostnameSource = "NETBIOS"

	// HostnameFromDNSSD is the name that a device gives the services that it
	// advertises over DNS-SD, such as "Living Room TV". A sweep finds it when
	// it browses the services over mDNS. It is a label for people, so it can
	// contain spaces.
	HostnameFromDNSSD HostnameSource = "DNSSD"

	// HostnameFromSSDP is the friendlyName in the UPnP description of a device
	// that answered the SSDP search of a sweep, such as "Living Room TV". It
	// is a label for people, so it can contain spaces.
	HostnameFromSSDP HostnameSource = "SSDP"
)

var hostnameSources = []HostnameSource{
	HostnameFromDNS,
	HostnameFromDHCPStatic,
	HostnameFromDHCPLease,
	HostnameFromMDNS,
	HostnameFromNetBIOS,
	HostnameFromDNSSD,
	HostnameFromSSDP,
}

// Valid reports whether s is one of the known hostname sources.
func (s HostnameSource) Valid() bool { return slices.Contains(hostnameSources, s) }

// Rank orders the origins of a name. The higher rank wins. Reverse DNS is
// first, because it is the name that resolves. A DHCP host-name is the claim
// of the client about itself, with a static lease or not. It can resolve to
// nothing. If the resolver serves the leases of the router, the PTR is that
// same name with the domain. So DNS keeps the longer spelling.
//
// An mDNS name ranks above a dynamic lease, because it resolves on the local
// network. A lease name can resolve to nothing. An mDNS name ranks below a
// static lease, because an operator chose that name.
//
// A NetBIOS name ranks last of these. It has at most 15 characters. It is
// often a cut version of the name that the device gives elsewhere. It
// resolves only where NetBIOS name service still runs.
//
// A DNS-SD service name and an SSDP friendlyName rank below all of them.
// Each is a label for people, and not a host name. The DNS-SD name ranks
// higher. The device announces it itself, but an SSDP name needs a fetch from
// a URL that the device gives.
//
// An unknown standing ranks zero, so a known name still beats an unknown one.
func (s HostnameSource) Rank() int {
	switch s {
	case HostnameFromDNS:
		return 7
	case HostnameFromDHCPStatic:
		return 6
	case HostnameFromMDNS:
		return 5
	case HostnameFromDHCPLease:
		return 4
	case HostnameFromNetBIOS:
		return 3
	case HostnameFromDNSSD:
		return 2
	case HostnameFromSSDP:
		return 1
	default:
		return 0
	}
}

// Swept reports whether a sweep learns names of standing s. A sweep learns
// such a name from the device or the resolver, for the address that
// answered.
func (s HostnameSource) Swept() bool {
	switch s {
	case HostnameFromDNS, HostnameFromMDNS, HostnameFromNetBIOS, HostnameFromDNSSD, HostnameFromSSDP:
		return true
	default:
		return false
	}
}

// Value renders s for the driver. It stores the empty source as null.
func (s HostnameSource) Value() (driver.Value, error) {
	if s == "" {
		return nil, nil
	}

	return enumValue(s, hostnameSources, "hostname source")
}

// Scan reads a stored hostname source into s. It reads null as empty.
func (s *HostnameSource) Scan(src any) error {
	if src == nil {
		*s = ""

		return nil
	}

	return enumScan(s, hostnameSources, "hostname source", src)
}

// TokenScope is what an API token can do. It is a named value. So a third
// scope can come without a column rename.
type TokenScope string

// TokenScope values for an API token.
const (
	// TokenRead allows any GET route.
	TokenRead TokenScope = "read"

	// TokenReadWrite also allows a route that changes something.
	TokenReadWrite TokenScope = "read_write"
)

var tokenScopes = []TokenScope{TokenRead, TokenReadWrite}

// Valid reports whether s is one of the known token scopes.
func (s TokenScope) Valid() bool { return slices.Contains(tokenScopes, s) }

// Value renders s for the driver. It refuses a value that the column does not
// allow.
func (s TokenScope) Value() (driver.Value, error) { return enumValue(s, tokenScopes, "token scope") }

// Scan reads a stored token scope into s.
func (s *TokenScope) Scan(src any) error { return enumScan(s, tokenScopes, "token scope", src) }

// UserRole is what an account can do.
type UserRole string

// UserRole values for an account.
const (
	// RoleAdmin also allows the management of other accounts.
	RoleAdmin UserRole = "admin"

	// RoleRead allows reads of the inventory, but not changes.
	RoleRead UserRole = "read"

	// RoleReadWrite also allows changes to the inventory.
	RoleReadWrite UserRole = "read_write"
)

var userRoles = []UserRole{RoleAdmin, RoleRead, RoleReadWrite}

// Valid reports whether r is one of the known user roles.
func (r UserRole) Valid() bool { return slices.Contains(userRoles, r) }

// Value renders r for the driver. It refuses a value that the column does not
// allow.
func (r UserRole) Value() (driver.Value, error) { return enumValue(r, userRoles, "user role") }

// Scan reads a stored user role into r.
func (r *UserRole) Scan(src any) error { return enumScan(r, userRoles, "user role", src) }

// rank orders the roles, so AtLeast can compare them. An unknown role ranks
// below every named role, so it passes no gate. This includes the zero
// value.
func (r UserRole) rank() int {
	switch r {
	case RoleRead:
		return 1
	case RoleReadWrite:
		return 2
	case RoleAdmin:
		return 3
	default:
		return 0
	}
}

// AtLeast reports whether r reaches want on the read < read_write < admin
// ladder. So a route can name the lowest role that it needs, and a higher role
// also passes.
func (r UserRole) AtLeast(want UserRole) bool { return r.rank() >= want.rank() && r.rank() > 0 }

// CanWrite reports whether an account with this role can change the
// inventory.
func (r UserRole) CanWrite() bool { return r.AtLeast(RoleReadWrite) }

// enumValue renders v. It refuses a value that the column does not allow. It
// also refuses the zero value. A column without its constant is a bug that
// needs an error. An empty string in the table would hide it.
func enumValue[T ~string](v T, admitted []T, name string) (driver.Value, error) {
	if !slices.Contains(admitted, v) {
		return nil, fmt.Errorf("dbtype: %q is not a valid %s", string(v), name)
	}

	return string(v), nil
}

// enumScan reads a stored value. It refuses a value that this build does not
// know. If the schema has a CHECK, the CHECK already refused such a value. If
// it does not, or the row is older than a rename, this is the only check.
func enumScan[T ~string](dst *T, admitted []T, name string, src any) error {
	s, err := text(src)
	if err != nil {
		return err
	}

	v := T(s)
	if !slices.Contains(admitted, v) {
		return fmt.Errorf("dbtype: %q is not a valid %s", s, name)
	}

	*dst = v

	return nil
}
