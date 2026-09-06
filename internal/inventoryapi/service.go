package inventoryapi

import (
	"cmp"
	"context"
	"fmt"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// InputError describes an argument the inventory cannot use.
type InputError struct{ Detail string }

func (e *InputError) Error() string { return e.Detail }

// Service presents recorded inventory without depending on a transport.
type Service struct{ store *inventory.Store }

// New builds operations over the application's shared store.
func New(store *inventory.Store) *Service { return &Service{store: store} }

// DevicesResult contains matching device summaries.
type DevicesResult struct {
	Devices []*inventory.Device `json:"devices"`
	Count   int                 `json:"count"`
}

// NetworksResult contains recorded networks, including empty ones.
type NetworksResult struct {
	Networks []*inventory.Network `json:"networks"`
	Count    int                  `json:"count"`
}

// GroupsResult contains assigned groups.
type GroupsResult struct {
	Groups []string `json:"groups"`
}

// PortsResult contains recorded TCP observations.
type PortsResult struct {
	Ports []*inventory.Port `json:"ports"`
	Count int               `json:"count"`
}

// SourcesResult contains the claims made by discovery sources.
type SourcesResult struct {
	Sources []*inventory.Claim `json:"sources"`
	Count   int                `json:"count"`
}

// EventsResult contains one page of the change log.
type EventsResult struct {
	Events     []*inventory.Event `json:"events"`
	Count      int                `json:"count"`
	NextCursor *inventory.Cursor  `json:"next_cursor,omitempty"`
}

// ScansResult contains one page of scan history.
type ScansResult struct {
	Scans      []*inventory.Scan `json:"scans"`
	Count      int               `json:"count"`
	NextCursor *inventory.Cursor `json:"next_cursor,omitempty"`
}

// ListDevices returns matching summaries, including current networks and ports.
func (s *Service) ListDevices(ctx context.Context, in Devices) (*DevicesResult, error) {
	if in.NetworkID != 0 {
		if _, err := s.store.Network(ctx, in.NetworkID); err != nil {
			return nil, err
		}
	}

	devices, err := s.store.ListDevices(ctx, inventory.DeviceFilter{
		Query: in.Q, Group: in.Group, Status: inventory.Status(in.Status), Sort: inventory.Sort(in.Sort),
		IncludeIgnored: in.IncludeIgnored, Network: in.NetworkID, Type: classify.Class(in.Type),
	})
	if err != nil {
		return nil, err
	}

	return &DevicesResult{Devices: devices, Count: len(devices)}, nil
}

// GetDevice returns full device details and address and port observations.
func (s *Service) GetDevice(ctx context.Context, in DeviceID) (*inventory.Device, error) {
	if err := positiveID(in.ID); err != nil {
		return nil, err
	}

	return s.store.Device(ctx, in.ID)
}

// GetStats returns inventory counts.
func (s *Service) GetStats(ctx context.Context, _ Empty) (*inventory.Stats, error) {
	return s.store.Stats(ctx)
}

// ListGroups returns assigned groups.
func (s *Service) ListGroups(ctx context.Context, _ Empty) (*GroupsResult, error) {
	groups, err := s.store.Groups(ctx)
	if err != nil {
		return nil, err
	}

	if groups == nil {
		groups = []string{}
	}

	return &GroupsResult{Groups: groups}, nil
}

// ListNetworks returns networks with device counts.
func (s *Service) ListNetworks(ctx context.Context, _ Empty) (*NetworksResult, error) {
	networks, err := s.store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	return &NetworksResult{Networks: networks, Count: len(networks)}, nil
}

// GetNetwork returns a recorded network.
func (s *Service) GetNetwork(ctx context.Context, in NetworkID) (*inventory.Network, error) {
	if err := positiveID(in.ID); err != nil {
		return nil, err
	}

	return s.store.Network(ctx, in.ID)
}

// GetDevicePorts returns recorded ports, optionally restricted to one state.
func (s *Service) GetDevicePorts(ctx context.Context, in Ports) (*PortsResult, error) {
	device, err := s.GetDevice(ctx, in.DeviceID)
	if err != nil {
		return nil, err
	}

	ports := make([]*inventory.Port, 0, len(device.Ports))
	for _, port := range device.Ports {
		if in.State == "" || string(port.State) == in.State {
			ports = append(ports, port)
		}
	}

	return &PortsResult{Ports: ports, Count: len(ports)}, nil
}

// GetPortOverview returns service counts and transitions in the last 24 hours.
func (s *Service) GetPortOverview(ctx context.Context, in Overview) (*inventory.PortOverview, error) {
	return s.store.PortOverview(ctx, cmp.Or(in.ServiceLimit, 10))
}

// GetDeviceSources returns source claims after verifying the device exists.
func (s *Service) GetDeviceSources(ctx context.Context, in DeviceID) (*SourcesResult, error) {
	if _, err := s.GetDevice(ctx, in); err != nil {
		return nil, err
	}

	sources, err := s.store.DeviceSources(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	return &SourcesResult{Sources: sources, Count: len(sources)}, nil
}

// ListEvents returns the next page matching the filters.
func (s *Service) ListEvents(ctx context.Context, in Events) (*EventsResult, error) {
	page, err := in.page()
	if err != nil {
		return nil, err
	}

	if in.DeviceID != 0 {
		if _, err := s.GetDevice(ctx, DeviceID{ID: in.DeviceID}); err != nil {
			return nil, err
		}
	}

	page.Device = in.DeviceID

	page.ExcludeIgnored = in.ExcludeIgnored
	for _, raw := range in.EventKinds {
		kind := dbtype.EventKind(raw)
		if !kind.Valid() {
			return nil, &InputError{Detail: fmt.Sprintf("unknown event kind %q", raw)}
		}

		page.EventKinds = append(page.EventKinds, kind)
	}

	result, err := s.store.ListEvents(ctx, page)
	if err != nil {
		return nil, err
	}

	return &EventsResult{Events: result.Events, Count: len(result.Events), NextCursor: nextCursor(result.Next)}, nil
}

// GetDeviceEvents returns one device's history with cursor pagination.
func (s *Service) GetDeviceEvents(ctx context.Context, in DeviceEvents) (*EventsResult, error) {
	if err := positiveID(in.ID); err != nil {
		return nil, err
	}

	result, err := s.ListEvents(ctx, Events{Pagination: in.Pagination, DeviceID: in.ID})
	if err != nil {
		return nil, err
	}

	for _, event := range result.Events {
		event.DeviceName = ""
	}

	return result, nil
}

// ListScans returns a page of recorded scan runs; it does not start a scan.
func (s *Service) ListScans(ctx context.Context, in Scans) (*ScansResult, error) {
	page, err := in.page()
	if err != nil {
		return nil, err
	}

	page.ScanKind = dbtype.ScanKind(in.Kind)

	result, err := s.store.ListScans(ctx, page)
	if err != nil {
		return nil, err
	}

	return &ScansResult{Scans: result.Scans, Count: len(result.Scans), NextCursor: nextCursor(result.Next)}, nil
}

// UpdateDeviceCuration replaces the user-owned fields and records changes.
func (s *Service) UpdateDeviceCuration(ctx context.Context, in Update) (*inventory.Device, error) {
	if err := positiveID(in.ID); err != nil {
		return nil, err
	}

	return s.store.UpdateCuration(ctx, in.ID, inventory.Curation{
		Label: in.Label, Notes: in.Notes, Group: in.Group, Type: in.Type, Ignored: in.Ignored,
	})
}

func positiveID(id int64) error {
	if id < 1 {
		return &InputError{Detail: "ID must be a positive whole number."}
	}

	return nil
}

func (p Pagination) page() (inventory.Page, error) {
	page := inventory.Page{Limit: cmp.Or(p.Limit, 50)}
	if err := page.Cursor.UnmarshalText([]byte(p.Cursor)); err != nil {
		return inventory.Page{}, &InputError{Detail: "invalid cursor: use next_cursor from the previous page"}
	}

	return page, nil
}

func nextCursor(c inventory.Cursor) *inventory.Cursor {
	if c.IsZero() {
		return nil
	}

	return &c
}
