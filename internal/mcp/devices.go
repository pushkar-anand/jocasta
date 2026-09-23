package mcp

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// listDevicesInput narrows a device list. Every field is optional; an empty
// call lists every device that is not ignored.
type listDevicesInput struct {
	Q              string `json:"q,omitempty" jsonschema:"Case-insensitive substring matched against each device's label, hostname, vendor, hardware (MAC) address and current IP addresses."`
	Group          string `json:"group,omitempty" jsonschema:"Only devices in this group, exactly as the user named it."`
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
func listDevices(store *inventory.Store) func(*mcpsdk.Server) {
	t := &mcpsdk.Tool{
		Name:  "list_devices",
		Title: "List devices",
		Description: "List the devices in the network inventory, optionally filtered by a search term, group or online status. " +
			"Each device carries its id, hardware address, current IP addresses, vendor, hostname, class, open ports, " +
			"when it was first and last seen, and the label, group and notes its owner gave it.",
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
			Status:         inventory.Status(in.Status),
			Sort:           inventory.Sort(in.Sort),
			IncludeIgnored: in.IncludeIgnored,
		})
		if err != nil {
			return nil, listDevicesOutput{}, err
		}

		return nil, listDevicesOutput{Devices: devices, Count: len(devices)}, nil
	}

	return func(s *mcpsdk.Server) { mcpsdk.AddTool(s, t, handler) }
}

// listDevicesSchema is the schema inferred from listDevicesInput, with the
// closed sets its two string filters admit spelled out, so the model is shown
// the choices rather than left to guess them, and the SDK turns away anything
// else before the handler runs.
//
// The values are the ones inventory.Status and inventory.Sort name, less the
// empty string each uses for "unset" -- here that is the field left out.
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

	return s
}
