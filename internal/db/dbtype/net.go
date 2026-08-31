package dbtype

import (
	"database/sql/driver"
	"fmt"
	"net"
	"net/netip"
)

// Addr is an IP address column.
type Addr struct {
	netip.Addr
}

// NewAddr returns a as an address column. A 4-in-6 address is unmapped, so the
// one address does not reach the table as both ::ffff:192.0.2.1 and 192.0.2.1.
func NewAddr(a netip.Addr) Addr {
	return Addr{a.Unmap()}
}

// Value renders a for the driver, refusing an invalid address.
func (a Addr) Value() (driver.Value, error) {
	if !a.IsValid() {
		return nil, fmt.Errorf("dbtype: refusing to store an invalid address")
	}

	return a.String(), nil
}

// Scan reads a stored address back into a.
func (a *Addr) Scan(src any) error {
	s, err := text(src)
	if err != nil {
		return err
	}

	parsed, err := netip.ParseAddr(s)
	if err != nil {
		return fmt.Errorf("dbtype: parse address %q: %w", s, err)
	}

	a.Addr = parsed.Unmap()

	return nil
}

// Prefix is a network column.
type Prefix struct {
	netip.Prefix
}

// NewPrefix returns p as a network column, masked to its base address so the
// one network cannot arrive as both 192.0.2.0/24 and 192.0.2.5/24.
func NewPrefix(p netip.Prefix) Prefix {
	return Prefix{p.Masked()}
}

// Value renders p for the driver, refusing an invalid prefix.
func (p Prefix) Value() (driver.Value, error) {
	if !p.IsValid() {
		return nil, fmt.Errorf("dbtype: refusing to store an invalid prefix")
	}

	return p.String(), nil
}

// Scan reads a stored network back into p.
func (p *Prefix) Scan(src any) error {
	s, err := text(src)
	if err != nil {
		return err
	}

	parsed, err := netip.ParsePrefix(s)
	if err != nil {
		return fmt.Errorf("dbtype: parse prefix %q: %w", s, err)
	}

	p.Prefix = parsed.Masked()

	return nil
}

// MAC is a hardware address column. Its zero value is the null one, which the
// schema uses for a device whose hardware has not been seen yet, so no separate
// null type is needed.
type MAC struct {
	net.HardwareAddr
}

// ParseMAC returns the hardware address s names. Only a 6-byte address is
// accepted, matching the column, and the returned MAC renders in the lowercase
// colon-separated form its CHECK admits.
func ParseMAC(s string) (MAC, error) {
	hw, err := net.ParseMAC(s)
	if err != nil {
		return MAC{}, fmt.Errorf("dbtype: parse hardware address %q: %w", s, err)
	}

	if len(hw) != 6 {
		return MAC{}, fmt.Errorf("dbtype: %q is %d bytes, not a 6-byte hardware address", s, len(hw))
	}

	return MAC{hw}, nil
}

// Valid reports whether a hardware address is held.
func (m MAC) Valid() bool {
	return len(m.HardwareAddr) > 0
}

// Value renders m for the driver, storing the zero address as null.
func (m MAC) Value() (driver.Value, error) {
	if !m.Valid() {
		return nil, nil
	}

	return m.String(), nil
}

// Scan reads a stored hardware address back into m, treating null as empty.
func (m *MAC) Scan(src any) error {
	if src == nil {
		m.HardwareAddr = nil

		return nil
	}

	s, err := text(src)
	if err != nil {
		return err
	}

	parsed, err := ParseMAC(s)
	if err != nil {
		return err
	}

	*m = parsed

	return nil
}

func text(src any) (string, error) {
	switch v := src.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("dbtype: cannot scan %T into a text column", src)
	}
}
