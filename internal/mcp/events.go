package mcp

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// pageSize is the page a call to a paged tool that asks for none gets, and
// pageLimit the most one may ask for: the same window and ceiling
// GET /api/events and GET /api/scans use.
const (
	pageSize  = 50
	pageLimit = 500
)

// listEventsInput narrows and pages the change log. Every field is optional;
// an empty call is the first page of the whole log.
type listEventsInput struct {
	DeviceID       int64    `json:"device_id,omitempty" jsonschema:"Only this device's events, by the id list_devices reports."`
	Kinds          []string `json:"kinds,omitempty" jsonschema:"Only events of these kinds. Omit for every kind."`
	ExcludeIgnored bool     `json:"exclude_ignored,omitempty" jsonschema:"Leave out events about devices the owner marked as ignored."`
	Limit          int      `json:"limit,omitempty" jsonschema:"Most events to return. Defaults to 50."`
	Cursor         string   `json:"cursor,omitempty" jsonschema:"The next_cursor a previous list_events call returned, to read the page after it. Pass the other filters unchanged."`
}

// listEventsOutput is one page of the change log, the same shape
// GET /api/events answers with.
type listEventsOutput struct {
	Events []*inventory.Event `json:"events"`
	Count  int                `json:"count"`

	// NextCursor is what to pass as cursor for the page after this one, and is
	// absent once the log has been read to the end.
	NextCursor *inventory.Cursor `json:"next_cursor,omitempty"`
}

// listEvents is inventory.Store.ListEvents, offered as a tool.
func listEvents(store *inventory.Store) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "list_events",
		Title: "List changes",
		Description: "Read the change log: what the inventory recorded changing, newest first. " +
			"Use it to answer what is new or different on the network, across every device or for one. " +
			"Event kinds: " + eventKindList() + ". " +
			"DEVICE_CLASSIFIED carries the classifier's reasons in detail; DEVICE_EDITED is a change the owner made. " +
			"Events are recorded when a scan runs, so the log is only as current as the last scan. " +
			"To read further back, pass next_cursor as cursor with the other filters unchanged, until next_cursor is absent.",
		InputSchema:  listEventsSchema(),
		OutputSchema: schemaFor[listEventsOutput](),
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: new(false),
		},
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in listEventsInput,
	) (*mcpsdk.CallToolResult, listEventsOutput, error) {
		var cursor inventory.Cursor
		if err := cursor.Decode(in.Cursor); err != nil {
			return nil, listEventsOutput{}, badCursor("list_events")
		}

		// Asking for a device that does not exist is a 404, as it is for the
		// API. An empty page would read as "nothing changed".
		if in.DeviceID != 0 {
			if _, err := store.Device(ctx, in.DeviceID); err != nil {
				return nil, listEventsOutput{}, err
			}
		}

		kinds := make([]dbtype.EventKind, 0, len(in.Kinds))
		for _, k := range in.Kinds {
			kinds = append(kinds, dbtype.EventKind(k))
		}

		page, err := store.ListEvents(ctx, inventory.Page{
			Limit:          cmp.Or(in.Limit, pageSize),
			Cursor:         cursor,
			Device:         in.DeviceID,
			EventKinds:     kinds,
			ExcludeIgnored: in.ExcludeIgnored,
		})
		if err != nil {
			return nil, listEventsOutput{}, err
		}

		out := listEventsOutput{Events: page.Events, Count: len(page.Events)}
		if !page.Next.IsZero() {
			out.NextCursor = &page.Next
		}

		return nil, out, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// badCursor is the problem a cursor this server did not issue gets, from the
// paged tool named. It is a Problem already, so addTool passes it to the agent
// as it stands.
func badCursor(tool string) response.Problem {
	return response.NewProblem().
		WithStatus(http.StatusBadRequest).
		WithDetail("cursor is not one a previous " + tool + " call returned").
		Build()
}

// listEventsSchema is the schema inferred from listEventsInput, with the event
// kinds spelled out and the numbers held to what the log can serve.
func listEventsSchema() *jsonschema.Schema {
	s := schemaFor[listEventsInput]()

	s.Properties["device_id"].Minimum = new(1.0)
	s.Properties["limit"].Minimum = new(1.0)
	s.Properties["limit"].Maximum = new(float64(pageLimit))

	kinds := make([]any, 0, len(dbtype.EventKinds()))
	for _, k := range dbtype.EventKinds() {
		kinds = append(kinds, string(k))
	}

	s.Properties["kinds"].Items.Enum = kinds

	return s
}

// eventKindList names every event kind, for the tool's description.
func eventKindList() string {
	names := make([]string, 0, len(dbtype.EventKinds()))
	for _, k := range dbtype.EventKinds() {
		names = append(names, string(k))
	}

	return strings.Join(names, ", ")
}
