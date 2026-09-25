package mcp

import (
	"cmp"
	"context"
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// listScansInput narrows and pages the scan history. Every field is optional;
// an empty call is the first page of every scan.
type listScansInput struct {
	Kind   string `json:"kind,omitempty" jsonschema:"Only scans of this kind. Omit for every kind."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Most scans to return. Defaults to 50."`
	Cursor string `json:"cursor,omitempty" jsonschema:"The next_cursor a previous list_scans call returned, to read the page after it. Pass kind unchanged."`
}

// listScansOutput is one page of the scan history, the same shape
// GET /api/scans answers with.
type listScansOutput struct {
	Scans []*inventory.Scan `json:"scans"`
	Count int               `json:"count"`

	// NextCursor is what to pass as cursor for the page after this one, and is
	// absent once the history has been read to the end.
	NextCursor *inventory.Cursor `json:"next_cursor,omitzero"`
}

// listScans is inventory.Store.ListScans, offered as a tool.
func listScans(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "list_scans",
		Title: "List scans",
		Description: "Read the scan history, most recent first: what ran, from which source, on which network, " +
			"when it started and finished, whether it succeeded, and what it found. " +
			"Kinds: DISCOVERY is a sweep for devices and found counts hosts; PORTS is a port scan and found counts open ports; " +
			"IMPORT is a read of a source such as a router and found counts the records it returned. " +
			"A scan still RUNNING long after it started is one whose process died before it could finish. " +
			"Use it to tell how current the inventory is, or why a device was not seen. This tool does not start a scan. " +
			"To read further back, pass next_cursor as cursor with kind unchanged, until next_cursor is absent.",
		InputSchema:  listScansSchema(),
		OutputSchema: schemaFor[listScansOutput](),
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: new(false),
		},
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in listScansInput,
	) (*mcpsdk.CallToolResult, listScansOutput, error) {
		var cursor inventory.Cursor
		if err := cursor.Decode(in.Cursor); err != nil {
			return nil, listScansOutput{}, badCursor("list_scans")
		}

		page, err := store.ListScans(ctx, inventory.Page{
			Limit:    cmp.Or(in.Limit, pageSize),
			Cursor:   cursor,
			ScanKind: dbtype.ScanKind(in.Kind),
		})
		if err != nil {
			return nil, listScansOutput{}, err
		}

		out := listScansOutput{Scans: page.Scans, Count: len(page.Scans)}
		if !page.Next.IsZero() {
			out.NextCursor = &page.Next
		}

		return nil, out, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// listScansSchema is the schema inferred from listScansInput, with the scan
// kinds spelled out and the limit held to what the history can serve.
func listScansSchema() *jsonschema.Schema {
	s := schemaFor[listScansInput]()

	s.Properties["limit"].Minimum = new(1.0)
	s.Properties["limit"].Maximum = new(float64(pageLimit))

	kinds := make([]any, 0, len(dbtype.ScanKinds()))
	for _, k := range dbtype.ScanKinds() {
		kinds = append(kinds, string(k))
	}

	s.Properties["kind"].Enum = kinds

	return s
}
