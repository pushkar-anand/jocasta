package mcp

import (
	"context"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// listDevicesInput narrows a device list. Every field is optional; an empty
// call lists every device that is not ignored.
type listDevicesInput struct {
	Q              string `json:"q,omitempty" jsonschema:"Case-insensitive substring matched against each device's label, hostname, vendor, hardware (MAC) address and current IP addresses."`
	Group          string `json:"group,omitempty" jsonschema:"Only devices in this group, exactly as the user named it."`
	NetworkID      int64  `json:"network_id,omitempty" jsonschema:"Only devices holding a current address on this network, by the id list_networks reports."`
	Type           string `json:"type,omitempty" jsonschema:"Only devices of this class: the owner's type where they set one, the classifier's guess otherwise."`
	Status         string `json:"status,omitempty" jsonschema:"Only devices seen recently (online) or not (offline). Omit for both."`
	Sort           string `json:"sort,omitempty" jsonschema:"Order of the list. Defaults to last_seen, the most recently seen device first."`
	IncludeIgnored bool   `json:"include_ignored,omitempty" jsonschema:"Also list the devices the user marked as ignored."`
}

// listDevicesOutput is the same shape GET /api/devices answers with, so an
// agent and a script reading the inventory see one vocabulary.
type listDevicesOutput struct {
	Devices []*inventory.Device `json:"devices"`
	Count   int                 `json:"count"`
}

// listDevices is inventory.Store.ListDevices, offered as a tool.
func listDevices(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "list_devices",
		Title: "List devices",
		Description: "List the devices in the network inventory, optionally filtered by a search term, group, network, " +
			"device class or online status. " +
			"Each device carries its id, hardware address, current IP addresses, vendor, hostname, class, open port numbers, " +
			"when it was first and last seen, and the label, group and notes its owner gave it. " +
			"Devices the owner marked as ignored are left out unless include_ignored is set. " +
			"Use it to find a device and its id; use get_device for one device's address history, ports and sources.",
		InputSchema:  listDevicesSchema(),
		OutputSchema: schemaFor[listDevicesOutput](),
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: new(false),
		},
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in listDevicesInput,
	) (*mcpsdk.CallToolResult, listDevicesOutput, error) {
		devices, err := store.ListDevices(ctx, inventory.DeviceFilter{
			Query:          in.Q,
			Group:          in.Group,
			Network:        in.NetworkID,
			Type:           classify.Class(in.Type),
			Status:         inventory.Status(in.Status),
			Sort:           inventory.Sort(in.Sort),
			IncludeIgnored: in.IncludeIgnored,
		})
		if err != nil {
			return nil, listDevicesOutput{}, err
		}

		return nil, listDevicesOutput{Devices: devices, Count: len(devices)}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// listDevicesSchema is the schema inferred from listDevicesInput, with the
// closed sets its string filters admit spelled out, so the model is shown the
// choices rather than left to guess them, and the SDK turns away anything else
// before the handler runs.
//
// The values are the ones inventory.Status, inventory.Sort and the classifier
// name, less the empty string each uses for "unset" -- here that is the field
// left out.
func listDevicesSchema() *jsonschema.Schema {
	s := schemaFor[listDevicesInput]()

	s.Properties["status"].Enum = []any{
		string(inventory.StatusOnline),
		string(inventory.StatusOffline),
	}
	s.Properties["sort"].Enum = []any{
		string(inventory.SortLastSeen),
		string(inventory.SortName),
		string(inventory.SortAddress),
		string(inventory.SortType),
	}
	s.Properties["type"].Enum = classEnum()
	s.Properties["network_id"].Minimum = new(1.0)

	return s
}

// getDeviceInput names one device.
type getDeviceInput struct {
	ID int64 `json:"id" jsonschema:"The device's id, as list_devices reports it."`
}

// getDeviceOutput is one device in full, with what each source that reported it
// claims. The two are kept apart, as the device page shows them: the device is
// the settled picture, and the sources are the evidence behind it, which can
// disagree.
type getDeviceOutput struct {
	Device  *inventory.Device  `json:"device"`
	Sources []*inventory.Claim `json:"sources"`
}

// getDevice is inventory.Store.Device and DeviceSources, offered as one tool.
func getDevice(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "get_device",
		Title: "Get a device",
		Description: "Get one device in full: its identity, the label, group, type, notes and ignored flag its owner set, " +
			"its classification, every address it has held and when, and every TCP port a scan has recorded open, " +
			"with when each opened or closed. Also returns what each discovery source (a network sweep, a router's " +
			"ARP or DHCP table) claims about the device and when that source last saw it; sources can disagree. " +
			"Use list_events with this id for the device's history, including why its classification changed.",
		InputSchema:  getDeviceSchema(),
		OutputSchema: schemaFor[getDeviceOutput](),
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: new(false),
		},
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in getDeviceInput,
	) (*mcpsdk.CallToolResult, getDeviceOutput, error) {
		device, err := store.Device(ctx, in.ID)
		if err != nil {
			return nil, getDeviceOutput{}, err
		}

		sources, err := store.DeviceSources(ctx, in.ID)
		if err != nil {
			return nil, getDeviceOutput{}, err
		}

		return nil, getDeviceOutput{Device: device, Sources: sources}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// getDeviceSchema is the schema inferred from getDeviceInput, with ids held to
// the positive numbers the inventory issues.
func getDeviceSchema() *jsonschema.Schema {
	s := schemaFor[getDeviceInput]()
	s.Properties["id"].Minimum = new(1.0)

	return s
}

// classEnum is every device class the classifier knows, for a schema that
// takes one.
func classEnum() []any {
	classes := make([]any, 0, len(classify.Classes()))
	for _, c := range classify.Classes() {
		classes = append(classes, string(c))
	}

	return classes
}
