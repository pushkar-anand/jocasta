package dbtype

import (
	"database/sql/driver"
	"fmt"
	"slices"
)

// SourceKind is what sort of thing produced a fact, not which implementation
// did it: RouterOS is one router among the several this could speak to.
type SourceKind string

// SourceKind values a fact's provenance can take.
const (
	SourceSweep  SourceKind = "SWEEP"
	SourceRouter SourceKind = "ROUTER"
	SourceDNS    SourceKind = "DNS"
	SourceManual SourceKind = "MANUAL"
)

var sourceKinds = []SourceKind{SourceSweep, SourceRouter, SourceDNS, SourceManual}

// Valid reports whether k is one of the known source kinds.
func (k SourceKind) Valid() bool { return slices.Contains(sourceKinds, k) }

// Value renders k for the driver, refusing anything the column does not admit.
func (k SourceKind) Value() (driver.Value, error) { return enumValue(k, sourceKinds, "source kind") }

// Scan reads a stored source kind back into k.
func (k *SourceKind) Scan(src any) error { return enumScan(k, sourceKinds, "source kind", src) }

// IdentitySource is what a device row is keyed on. A device seen only over IP
// is provisional: the address it answered on is all that tells it apart, so it
// can still be folded into a hardware-identified device later.
type IdentitySource string

// IdentitySource values a device row's identity can take.
const (
	IdentityMAC IdentitySource = "MAC"
	IdentityIP  IdentitySource = "IP"
)

var identitySources = []IdentitySource{IdentityMAC, IdentityIP}

// Valid reports whether s is one of the known identity sources.
func (s IdentitySource) Valid() bool { return slices.Contains(identitySources, s) }

// Value renders s for the driver, refusing anything the column does not admit.
func (s IdentitySource) Value() (driver.Value, error) {
	return enumValue(s, identitySources, "identity source")
}

// Scan reads a stored identity source back into s.
func (s *IdentitySource) Scan(src any) error {
	return enumScan(s, identitySources, "identity source", src)
}

// ScanKind is what a scan set out to do.
type ScanKind string

// ScanKind values a scan's stated purpose can take.
const (
	ScanDiscovery ScanKind = "DISCOVERY"
	ScanPorts     ScanKind = "PORTS"
	ScanImport    ScanKind = "IMPORT"
)

var scanKinds = []ScanKind{ScanDiscovery, ScanPorts, ScanImport}

// Valid reports whether k is one of the known scan kinds.
func (k ScanKind) Valid() bool { return slices.Contains(scanKinds, k) }

// Value renders k for the driver, refusing anything the column does not admit.
func (k ScanKind) Value() (driver.Value, error) { return enumValue(k, scanKinds, "scan kind") }

// Scan reads a stored scan kind back into k.
func (k *ScanKind) Scan(src any) error { return enumScan(k, scanKinds, "scan kind", src) }

// ScanStatus is how a scan ended, or that it has not. A row left running is a
// scan whose process died before it could say otherwise.
type ScanStatus string

// ScanStatus values a scan's outcome can take.
const (
	StatusRunning   ScanStatus = "RUNNING"
	StatusOK        ScanStatus = "OK"
	StatusFailed    ScanStatus = "FAILED"
	StatusCancelled ScanStatus = "CANCELLED"
)

var scanStatuses = []ScanStatus{StatusRunning, StatusOK, StatusFailed, StatusCancelled}

// Valid reports whether s is one of the known scan statuses.
func (s ScanStatus) Valid() bool { return slices.Contains(scanStatuses, s) }

// Value renders s for the driver, refusing anything the column does not admit.
func (s ScanStatus) Value() (driver.Value, error) { return enumValue(s, scanStatuses, "scan status") }

// Scan reads a stored scan status back into s.
func (s *ScanStatus) Scan(src any) error { return enumScan(s, scanStatuses, "scan status", src) }

// PortState is what a probe last found at a port. A port that has answered once
// keeps its row when it goes silent, flipping to closed rather than vanishing,
// so the record of what a device used to expose survives the service stopping.
type PortState string

// PortState values a probed port can be recorded in.
const (
	PortOpen   PortState = "open"
	PortClosed PortState = "closed"
)

var portStates = []PortState{PortOpen, PortClosed}

// Valid reports whether s is one of the known port states.
func (s PortState) Valid() bool { return slices.Contains(portStates, s) }

// Value renders s for the driver, refusing anything the column does not admit.
func (s PortState) Value() (driver.Value, error) { return enumValue(s, portStates, "port state") }

// Scan reads a stored port state back into s.
func (s *PortState) Scan(src any) error { return enumScan(s, portStates, "port state", src) }

// EventKind is what changed. The column carries no CHECK, so that adding a kind
// stays a Go change rather than a migration, which leaves this list as the only
// thing keeping a typo out of the permanent record.
type EventKind string

// EventKind values a logged change can take.
const (
	EventDeviceDiscovered EventKind = "DEVICE_DISCOVERED"
	EventDeviceIdentified EventKind = "DEVICE_IDENTIFIED"
	EventDevicesMerged    EventKind = "DEVICES_MERGED"
	EventAddressAdded     EventKind = "ADDRESS_ADDED"
	EventHostnameChanged  EventKind = "HOSTNAME_CHANGED"

	// EventAddressReleased records a sweep concluding a device has moved off an
	// address: it answered elsewhere in the prefix while this one stayed silent
	// past the grace window.
	EventAddressReleased EventKind = "ADDRESS_RELEASED"

	// EventDeviceEdited records the user changing what they own on a device.
	// Their edits belong in the change log for the same reason a scan's do:
	// the log is the record of what changed, whoever changed it.
	EventDeviceEdited EventKind = "DEVICE_EDITED"

	// EventPortOpened and EventPortClosed record a port scan finding a device
	// listening where it was not, or gone quiet where it answered before.
	EventPortOpened EventKind = "PORT_OPENED"
	EventPortClosed EventKind = "PORT_CLOSED"

	// EventDeviceClassified records a scan's classifier moving its guess at
	// what kind of device this is from one known class to another. The first
	// guess is silent -- it is part of discovering the device -- and so is the
	// guess lapsing back to nothing. The user's own answer, in device_type, is
	// an edit, not this.
	EventDeviceClassified EventKind = "DEVICE_CLASSIFIED"
)

var eventKinds = []EventKind{
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

// Value renders k for the driver, refusing anything the column does not admit.
func (k EventKind) Value() (driver.Value, error) { return enumValue(k, eventKinds, "event kind") }

// Scan reads a stored event kind back into k.
func (k *EventKind) Scan(src any) error { return enumScan(k, eventKinds, "event kind", src) }

// HostnameSource names where a device's name came from. A sweep resolves names
// one way; other sources bring their own and have to be told apart. Its zero
// value is the null one, as MAC's is, for a device with no name yet.
type HostnameSource string

// HostnameSource values a device's name can be learned at.
const (
	// HostnameFromDNS is a name a sweep learned by resolving the address.
	HostnameFromDNS HostnameSource = "DNS"

	// HostnameFromDHCPStatic and HostnameFromDHCPLease are both the client's
	// own claim about itself. They rank differently because an operator bound
	// the static one deliberately, so its name is one they have already vetted.
	HostnameFromDHCPStatic HostnameSource = "DHCP_STATIC"
	HostnameFromDHCPLease  HostnameSource = "DHCP_LEASE"
)

var hostnameSources = []HostnameSource{HostnameFromDNS, HostnameFromDHCPStatic, HostnameFromDHCPLease}

// Valid reports whether s is one of the known hostname sources.
func (s HostnameSource) Valid() bool { return slices.Contains(hostnameSources, s) }

// Value renders s for the driver. The empty source is stored as null.
func (s HostnameSource) Value() (driver.Value, error) {
	if s == "" {
		return nil, nil
	}

	return enumValue(s, hostnameSources, "hostname source")
}

// Scan reads a stored hostname source back into s, treating null as empty.
func (s *HostnameSource) Scan(src any) error {
	if src == nil {
		*s = ""

		return nil
	}

	return enumScan(s, hostnameSources, "hostname source", src)
}

// TokenScope is what an API token is allowed to do. Named rather than a bool,
// so a third scope has somewhere to go without a column rename.
type TokenScope string

// TokenScope values an API token can carry.
const (
	// TokenRead admits any GET route.
	TokenRead TokenScope = "read"

	// TokenReadWrite additionally admits a route that changes something.
	TokenReadWrite TokenScope = "read_write"
)

var tokenScopes = []TokenScope{TokenRead, TokenReadWrite}

// Valid reports whether s is one of the known token scopes.
func (s TokenScope) Valid() bool { return slices.Contains(tokenScopes, s) }

// Value renders s for the driver, refusing anything the column does not admit.
func (s TokenScope) Value() (driver.Value, error) { return enumValue(s, tokenScopes, "token scope") }

// Scan reads a stored token scope back into s.
func (s *TokenScope) Scan(src any) error { return enumScan(s, tokenScopes, "token scope", src) }

// enumValue renders v, refusing anything the column does not admit. The zero
// value is refused with the rest: a column reached without its constant set is
// a bug worth a name, not an empty string in the table.
func enumValue[T ~string](v T, admitted []T, name string) (driver.Value, error) {
	if !slices.Contains(admitted, v) {
		return nil, fmt.Errorf("dbtype: %q is not a valid %s", string(v), name)
	}

	return string(v), nil
}

// enumScan reads a stored value back, refusing one this build does not know.
// Where the schema carries a CHECK it has already said the same thing; where it
// does not, or where the row predates a rename, this is the only check there is.
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
