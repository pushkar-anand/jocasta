package mcp

import (
	"context"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// explainDeviceInput names one device.
type explainDeviceInput struct {
	ID int64 `json:"id" jsonschema:"The device's id, as list_devices reports it."`
}

// explainDevice is inventory.Store.ExplainClass, offered as a tool.
func explainDevice(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "explain_device",
		Title: "Explain a device's class",
		Description: "Explain why a device has the class it has. Returns the class, the type its owner set and whether " +
			"that overrides the classifier's guess, then the guess and its confidence (low, medium or high). " +
			"Facts are what the classifier was given: vendor, hostname and where the hostname came from, whether the " +
			"hardware address is randomised, the name of a network the device is on, its current addresses and the TCP " +
			"ports open now. Rule is the rule that decided the guess, with its reason; other_rules are every other rule " +
			"that matched and lost, the most specific first. The rule that tests the most facts wins, a rule for the " +
			"same class raises the confidence, and a weak rule alone gives a low-confidence guess. " +
			"Use it when a class looks wrong or doubtful, to see which fact to question. " +
			"Every rule is a heuristic over names, vendors and ports, any of which a device can set. " +
			"The classifier reruns over what is recorded now, so the guess can differ from the device's recorded one " +
			"until a scan next touches it. Use list_events with kind DEVICE_CLASSIFIED for how the guess changed over time.",
		InputSchema:  explainDeviceSchema(),
		OutputSchema: schemaFor[inventory.ClassExplanation](),
		Annotations:  readOnly(),
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in explainDeviceInput,
	) (*mcpsdk.CallToolResult, *inventory.ClassExplanation, error) {
		out, err := store.ExplainClass(ctx, in.ID)
		if err != nil {
			return nil, nil, err
		}

		return nil, out, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// explainDeviceSchema is the schema inferred from explainDeviceInput, with ids
// held to the positive numbers the inventory issues.
func explainDeviceSchema() *jsonschema.Schema {
	s := schemaFor[explainDeviceInput]()
	s.Properties["id"].Minimum = new(1.0)

	return s
}
