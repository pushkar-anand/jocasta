package mcp

import (
	"context"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// updateDeviceCurationInput is a device, with every field that its owner
// controls.
//
// No field is optional. The call replaces all five. So an agent that left one
// out would clear it by mistake. Because each field is required, the SDK
// refuses such a call before it writes anything.
type updateDeviceCurationInput struct {
	ID      int64  `json:"id" jsonschema:"The device's id, as list_devices reports it."`
	Label   string `json:"label" jsonschema:"The name to show for the device. Empty clears it."`
	Group   string `json:"group" jsonschema:"The group to file the device under. Empty clears it."`
	Type    string `json:"type" jsonschema:"The device class, overriding the classifier's guess. Empty removes the override, so the guess applies again."`
	Notes   string `json:"notes" jsonschema:"Free-text notes. Empty clears them."`
	Ignored bool   `json:"ignored" jsonschema:"Whether to hide the device from default device lists. get_stats still includes ignored devices."`
}

// updateDeviceCurationOutput is the device after the change.
type updateDeviceCurationOutput struct {
	Device *inventory.Device `json:"device"`
}

// updateDeviceCuration is inventory.Store.UpdateCuration, as a tool. It
// changes the inventory, so only a read_write token gets it.
func updateDeviceCuration(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "update_device_curation",
		Title: "Update a device's label, group, type, notes and ignored flag",
		Description: "Set the five fields a device's owner controls: label, group, type, notes and ignored. " +
			"Every field is replaced, so call get_device first and pass back the current value of each field you are not changing. " +
			"An empty string clears a field; an empty type removes the owner's override and restores the classifier's guess. " +
			"Nothing a scan recorded (addresses, hostname, vendor, hardware address) can be changed. " +
			"Each change is recorded in the change log as DEVICE_EDITED. Returns the device as updated.",
		InputSchema:  updateDeviceCurationSchema(),
		OutputSchema: schemaFor[updateDeviceCurationOutput](),
		Annotations: &mcpsdk.ToolAnnotations{
			// It overwrites what the owner wrote. A second identical call
			// changes nothing more.
			DestructiveHint: new(true),
			IdempotentHint:  true,
			OpenWorldHint:   new(false),
		},
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in updateDeviceCurationInput,
	) (*mcpsdk.CallToolResult, updateDeviceCurationOutput, error) {
		device, err := store.UpdateCuration(ctx, in.ID, inventory.Curation{
			Label:   in.Label,
			Notes:   in.Notes,
			Group:   in.Group,
			Type:    in.Type,
			Ignored: in.Ignored,
		})
		if err != nil {
			return nil, updateDeviceCurationOutput{}, err
		}

		return nil, updateDeviceCurationOutput{Device: device}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// updateDeviceCurationSchema is the schema inferred from
// updateDeviceCurationInput. It limits the id to what the inventory gives, and
// the text fields to the lengths of the API. It limits type to the classes of
// the classifier, or the empty string that removes an override.
func updateDeviceCurationSchema() *jsonschema.Schema {
	s := schemaFor[updateDeviceCurationInput]()

	s.Properties["id"].Minimum = new(1.0)
	s.Properties["label"].MaxLength = new(inventory.LabelMaxLength)
	s.Properties["group"].MaxLength = new(inventory.GroupMaxLength)
	s.Properties["notes"].MaxLength = new(inventory.NotesMaxLength)

	s.Properties["type"].Enum = append([]any{""}, enumOf(classify.Classes())...)

	return s
}

// watchDeviceInput is a device, and whether its owner gets told when it goes
// quiet or comes back. Both are required. Without this, a call without watched
// would stop the watch.
type watchDeviceInput struct {
	ID      int64 `json:"id" jsonschema:"The device's id, as list_devices reports it."`
	Watched bool  `json:"watched" jsonschema:"True to watch the device, false to stop."`
}

// watchDeviceOutput is the device after the change.
type watchDeviceOutput struct {
	Device *inventory.Device `json:"device"`
}

// watchDevice is inventory.Store.Watch, as a tool. It changes the inventory,
// so only a read_write token gets it.
func watchDevice(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "watch_device",
		Title: "Watch a device, or stop",
		Description: "Set whether the owner is told when a device goes quiet (offline) or comes back. " +
			"A watched device logs DEVICE_QUIET when it has not been seen for longer than the online window, and DEVICE_BACK when it is seen again; " +
			"devices nobody watches log neither. The owner's notification settings decide whether either is sent anywhere. " +
			"Watching is apart from update_device_curation, which leaves it unchanged. " +
			"The change is recorded in the change log as DEVICE_EDITED. Returns the device as updated.",
		InputSchema:  watchDeviceSchema(),
		OutputSchema: schemaFor[watchDeviceOutput](),
		Annotations: &mcpsdk.ToolAnnotations{
			// It changes only a flag that the owner can set back. A second
			// identical call changes nothing more.
			DestructiveHint: new(false),
			IdempotentHint:  true,
			OpenWorldHint:   new(false),
		},
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in watchDeviceInput,
	) (*mcpsdk.CallToolResult, watchDeviceOutput, error) {
		device, err := store.Watch(ctx, in.ID, in.Watched)
		if err != nil {
			return nil, watchDeviceOutput{}, err
		}

		return nil, watchDeviceOutput{Device: device}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// watchDeviceSchema is the schema inferred from watchDeviceInput. It limits the
// id to what the inventory gives.
func watchDeviceSchema() *jsonschema.Schema {
	s := schemaFor[watchDeviceInput]()

	s.Properties["id"].Minimum = new(1.0)

	return s
}
