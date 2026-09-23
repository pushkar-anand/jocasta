package mcp

import (
	"context"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// The longest each free-text field may be, the same bounds PATCH
// /api/devices/{id} holds a body to.
const (
	labelMaxLength = 200
	groupMaxLength = 100
	notesMaxLength = 2000
)

// updateDeviceCurationInput is a device and every field its owner controls.
//
// No field is optional: the call replaces all five, so an agent that left one
// out would clear it without meaning to. Making each required has the SDK turn
// such a call away before anything is written.
type updateDeviceCurationInput struct {
	ID      int64  `json:"id" jsonschema:"The device's id, as list_devices reports it."`
	Label   string `json:"label" jsonschema:"The name to show for the device. Empty clears it."`
	Group   string `json:"group" jsonschema:"The group to file the device under. Empty clears it."`
	Type    string `json:"type" jsonschema:"The device class, overriding the classifier's guess. Empty removes the override, so the guess applies again."`
	Notes   string `json:"notes" jsonschema:"Free-text notes. Empty clears them."`
	Ignored bool   `json:"ignored" jsonschema:"Whether to hide the device from device lists and counts of what is online."`
}

// updateDeviceCurationOutput is the device as it stands after the change.
type updateDeviceCurationOutput struct {
	Device *inventory.Device `json:"device"`
}

// updateDeviceCuration is inventory.Store.UpdateCuration, offered as a tool.
// It is the one tool that changes the inventory, so only a read_write token is
// offered it.
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
			// It overwrites what the owner wrote, and a second identical call
			// changes nothing further.
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
// updateDeviceCurationInput, with the id held to what the inventory issues, the
// text fields to the API's lengths, and type to the classifier's classes or
// the empty string that removes an override.
func updateDeviceCurationSchema() *jsonschema.Schema {
	s := schemaFor[updateDeviceCurationInput]()

	s.Properties["id"].Minimum = new(1.0)
	s.Properties["label"].MaxLength = new(labelMaxLength)
	s.Properties["group"].MaxLength = new(groupMaxLength)
	s.Properties["notes"].MaxLength = new(notesMaxLength)

	s.Properties["type"].Enum = append([]any{""}, classEnum()...)

	return s
}
