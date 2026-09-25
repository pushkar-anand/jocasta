package mcp

import (
	"cmp"
	"context"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// serviceLimit is how many services a call that asks for no particular number
// gets, and serviceLimitMax the most one may ask for.
const (
	serviceLimit    = 10
	serviceLimitMax = 100
)

// getPortOverviewInput bounds the service breakdown.
type getPortOverviewInput struct {
	ServiceLimit int `json:"service_limit,omitempty" jsonschema:"Most services to list, commonest first. Defaults to 10."`
}

// getPortOverviewOutput wraps the overview so the result is an object with a
// name, as every other tool's is.
type getPortOverviewOutput struct {
	Overview *inventory.PortOverview `json:"overview"`
}

// getPortOverview is inventory.Store.PortOverview, offered as a tool.
func getPortOverview(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "get_port_overview",
		Title: "Summarise open ports",
		Description: "Summarise the open TCP ports across the network: how many are open now and on how many devices, " +
			"how many opened and closed in the last 24 hours, and the commonest services with how many devices offer each. " +
			"Ignored devices are left out. A service name is the service usually found on that port number; Jocasta does not " +
			"detect the software behind it. A port with no usual service is listed by number alone. " +
			"Use get_device for one device's ports, and list_events with PORT_OPENED or PORT_CLOSED for which ports changed.",
		InputSchema:  getPortOverviewSchema(),
		OutputSchema: schemaFor[getPortOverviewOutput](),
		Annotations:  readOnly(),
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in getPortOverviewInput,
	) (*mcpsdk.CallToolResult, getPortOverviewOutput, error) {
		overview, err := store.PortOverview(ctx, cmp.Or(in.ServiceLimit, serviceLimit))
		if err != nil {
			return nil, getPortOverviewOutput{}, err
		}

		return nil, getPortOverviewOutput{Overview: overview}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// getPortOverviewSchema is the schema inferred from getPortOverviewInput, with
// the limit held to what the overview can serve.
func getPortOverviewSchema() *jsonschema.Schema {
	s := schemaFor[getPortOverviewInput]()
	s.Properties["service_limit"].Minimum = new(1.0)
	s.Properties["service_limit"].Maximum = new(float64(serviceLimitMax))

	return s
}
