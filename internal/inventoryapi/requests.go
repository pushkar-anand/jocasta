// Package inventoryapi defines the inventory operations shared by JSON and MCP.
// Transports decode and validate these inputs before calling the service.
package inventoryapi

// Empty is the input for operations without arguments.
type Empty struct{}

// DeviceID identifies a recorded device.
type DeviceID struct {
	ID int64 `json:"id" schema:"-" validate:"omitempty,min=1" jsonschema:"Positive device ID from list_devices"`
}

// SetID supplies the ID decoded from an HTTP route.
func (p *DeviceID) SetID(id int64) { p.ID = id }

// Devices narrows the inventory using the same filters on every transport.
type Devices struct {
	Q              string `json:"q,omitempty" schema:"q"`
	Group          string `json:"group,omitempty" schema:"group"`
	Status         string `json:"status,omitempty" schema:"status" validate:"omitempty,oneof=online offline" jsonschema:"online or offline; based on the configured observation window"`
	Sort           string `json:"sort,omitempty" schema:"sort" validate:"omitempty,oneof=last_seen name address type" jsonschema:"Sort by last_seen (default), name, address, or type"`
	IncludeIgnored bool   `json:"include_ignored,omitempty" schema:"include_ignored"`
	NetworkID      int64  `json:"network_id,omitempty" schema:"network_id" validate:"omitempty,min=1"`
	Type           string `json:"type,omitempty" schema:"type" validate:"omitempty,deviceclass" jsonschema:"Device class: router, switch, access_point, firewall, server, nas, hypervisor, desktop, laptop, phone, tablet, printer, camera, tv, streaming, speaker, voice_assistant, game_console, iot_hub, smart_home, wearable, voip; empty means no override or filter"`
}

// Pagination requests a bounded window of history.
type Pagination struct {
	Limit  int    `json:"limit,omitempty" schema:"limit" validate:"omitempty,min=1,max=500" jsonschema:"Page size from 1 to 500; defaults to 50"`
	Cursor string `json:"cursor,omitempty" schema:"cursor" jsonschema:"Opaque next_cursor from the previous page with the same filters"`
}

// Events filters the change log.
type Events struct {
	Pagination
	DeviceID       int64    `json:"device_id,omitempty" schema:"device_id" validate:"omitempty,min=1"`
	EventKinds     []string `json:"event_kinds,omitempty" schema:"event_kinds"`
	ExcludeIgnored bool     `json:"exclude_ignored,omitempty" schema:"exclude_ignored"`
}

// DeviceEvents requests history belonging to one device.
type DeviceEvents struct {
	DeviceID
	Pagination
}

// Scans filters scan history by kind.
type Scans struct {
	Pagination
	Kind string `json:"kind,omitempty" schema:"kind" validate:"omitempty,oneof=DISCOVERY PORTS IMPORT"`
}

// NetworkID identifies a recorded network.
type NetworkID struct {
	ID int64 `json:"id" schema:"-" validate:"omitempty,min=1" jsonschema:"Positive network ID from list_networks"`
}

// SetID supplies the ID decoded from an HTTP route.
func (p *NetworkID) SetID(id int64) { p.ID = id }

// Ports selects the recorded TCP states for one device.
type Ports struct {
	DeviceID
	State string `json:"state,omitempty" schema:"state" validate:"omitempty,oneof=open closed"`
}

// Overview bounds the common-service breakdown.
type Overview struct {
	ServiceLimit int `json:"service_limit,omitempty" schema:"service_limit" validate:"omitempty,min=1,max=100" jsonschema:"Number of common services from 1 to 100; defaults to 10"`
}

// Curation replaces every user-owned field. MCP requires every field explicitly;
// REST preserves its existing behavior of clearing omitted fields.
type Curation struct {
	Label   string `json:"label" validate:"omitempty,max=200"`
	Notes   string `json:"notes" validate:"omitempty,max=2000"`
	Group   string `json:"group" validate:"omitempty,max=100"`
	Type    string `json:"type" validate:"omitempty,deviceclass" jsonschema:"Device class: router, switch, access_point, firewall, server, nas, hypervisor, desktop, laptop, phone, tablet, printer, camera, tv, streaming, speaker, voice_assistant, game_console, iot_hub, smart_home, wearable, voip; empty means no override or filter"`
	Ignored bool   `json:"ignored"`
}

// Update identifies a device and its replacement curation.
type Update struct {
	DeviceID
	Curation
}
