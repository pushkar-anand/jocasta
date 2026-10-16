package mcp

import (
	"context"
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// getStatsOutput has the same shape as the answer of GET /api/stats.
type getStatsOutput struct {
	*inventory.Stats
}

// getStats is inventory.Store.Stats, as a tool.
func getStats(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "get_stats",
		Title: "Count devices",
		Description: "Count the devices in the inventory: the total, how many were seen recently or are quiet, how many the owner " +
			"marked as ignored, and how many were first discovered in the last 24 hours. " +
			"Use it for an inventory summary; use list_devices to identify individual devices. " +
			"The counts include ignored devices and reflect past scans, not live reachability.",
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

// listGroupsOutput has the same shape as the answer of GET /api/groups.
type listGroupsOutput struct {
	Groups []string `json:"groups"`
}

// listGroups is inventory.Store.Groups, as a tool.
func listGroups(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "list_groups",
		Title: "List groups",
		Description: "List the group names the owner has filed devices under. " +
			"Use it to choose a group filter, then pass a name to list_devices as group to see its devices. " +
			"It returns names only, without device counts; groups with no assigned devices are absent.",
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

		// With no group yet, the list is empty. So the result matches its
		// schema.
		if groups == nil {
			groups = []string{}
		}

		return nil, listGroupsOutput{Groups: groups}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}
