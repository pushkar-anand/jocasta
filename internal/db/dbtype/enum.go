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

	// EventDeviceEdited records the user changing what they own on a device.
	// Their edits belong in the change log for the same reason a scan's do:
	// the log is the record of what changed, whoever changed it.
	EventDeviceEdited EventKind = "DEVICE_EDITED"
)

var eventKinds = []EventKind{
	EventDeviceDiscovered,
	EventDeviceIdentified,
	EventDevicesMerged,
	EventAddressAdded,
	EventHostnameChanged,
	EventDeviceEdited,
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

// HostnameFromDNS marks a name that a sweep learned by resolving the address.
const HostnameFromDNS HostnameSource = "DNS"

var hostnameSources = []HostnameSource{HostnameFromDNS}

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
