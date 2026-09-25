package mcp

import (
	"context"
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// getStatsOutput is the same shape GET /api/stats answers with.
type getStatsOutput struct {
	*inventory.Stats
}

// getStats is inventory.Store.Stats, offered as a tool.
func getStats(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "get_stats",
		Title: "Count devices",
		Description: "Count the devices in the inventory: the total, how many are online and offline, how many the owner " +
			"marked as ignored, and how many were first discovered in the last 24 hours. " +
			"Unlike list_devices, the counts include ignored devices.",
		InputSchema:  schemaFor[struct{}](),
		OutputSchema: schemaFor[getStatsOutput](),
		Annotations:  readOnly(),
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		_ struct{},
	) (*mcpsdk.CallToolResult, getStatsOutput, error) {
		stats, err := store.Stats(ctx)
		if err != nil {
			return nil, getStatsOutput{}, err
		}

		return nil, getStatsOutput{Stats: stats}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// listGroupsOutput is the same shape GET /api/groups answers with.
type listGroupsOutput struct {
	Groups []string `json:"groups"`
}

// listGroups is inventory.Store.Groups, offered as a tool.
func listGroups(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "list_groups",
		Title: "List groups",
		Description: "List the group names the owner has filed devices under. " +
			"Pass one to list_devices as group to see its devices.",
		InputSchema:  schemaFor[struct{}](),
		OutputSchema: schemaFor[listGroupsOutput](),
		Annotations:  readOnly(),
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		_ struct{},
	) (*mcpsdk.CallToolResult, listGroupsOutput, error) {
		groups, err := store.Groups(ctx)
		if err != nil {
			return nil, listGroupsOutput{}, err
		}

		// No group assigned yet is an empty list, so the result matches its
		// schema.
		if groups == nil {
			groups = []string{}
		}

		return nil, listGroupsOutput{Groups: groups}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}
