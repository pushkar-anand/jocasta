package mcp

import (
	"context"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// listNetworksOutput is every recorded network, each with its device counts.
type listNetworksOutput struct {
	Networks []*inventory.Network `json:"networks"`
	Count    int                  `json:"count"`
}

// listNetworks is inventory.Store.ListNetworks, offered as a tool.
func listNetworks(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "list_networks",
		Title: "List networks",
		Description: "List the networks the inventory has recorded: each one's id, prefix, name and VLAN tag where a router " +
			"reported them, and how many devices hold a current address on it, online and offline. " +
			"A network nothing has been found on is listed at zero. " +
			"Pass a network's id to list_devices as network_id to see the devices on it.",
		InputSchema:  schemaFor[struct{}](),
		OutputSchema: schemaFor[listNetworksOutput](),
		Annotations:  readOnly(),
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		_ struct{},
	) (*mcpsdk.CallToolResult, listNetworksOutput, error) {
		networks, err := store.ListNetworks(ctx)
		if err != nil {
			return nil, listNetworksOutput{}, err
		}

		return nil, listNetworksOutput{Networks: networks, Count: len(networks)}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// getNetworkInput names one network.
type getNetworkInput struct {
	ID int64 `json:"id" jsonschema:"The network's id, as list_networks reports it."`
}

// getNetworkOutput wraps the network so the result is an object with a name,
// as every other tool's is.
type getNetworkOutput struct {
	Network *inventory.Network `json:"network"`
}

// getNetwork is inventory.Store.Network, offered as a tool.
func getNetwork(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "get_network",
		Title: "Get a network",
		Description: "Get one recorded network by id: its prefix, name, VLAN tag and device counts. " +
			"Use list_devices with network_id for the devices on it.",
		InputSchema:  getNetworkSchema(),
		OutputSchema: schemaFor[getNetworkOutput](),
		Annotations:  readOnly(),
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in getNetworkInput,
	) (*mcpsdk.CallToolResult, getNetworkOutput, error) {
		network, err := store.Network(ctx, in.ID)
		if err != nil {
			return nil, getNetworkOutput{}, err
		}

		return nil, getNetworkOutput{Network: network}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// getNetworkSchema is the schema inferred from getNetworkInput, with ids held
// to the positive numbers the inventory issues.
func getNetworkSchema() *jsonschema.Schema {
	s := schemaFor[getNetworkInput]()
	s.Properties["id"].Minimum = new(1.0)

	return s
}
