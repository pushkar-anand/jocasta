package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// tool is one entry in what the server offers.
type tool struct {
	// writes marks a tool that changes the inventory. Only a read_write token
	// is shown one.
	writes bool

	// register adds the tool to a server, usually through addTool. It is
	// called once per server the tool belongs on.
	register func(*mcpsdk.Server, *slog.Logger)
}

// tools is every tool the server offers. A new tool is a file defining it and
// one line here.
func tools(store *inventory.Store) []tool {
	return []tool{
		{register: listDevices(store)},
		{register: getDevice(store)},
		{register: listEvents(store)},
		{register: listNetworks(store)},
		{register: getNetwork(store)},
		{register: getStats(store)},
		{register: listGroups(store)},
		{register: getPortOverview(store)},
		{register: listScans(store)},
		{register: listTraffic(store, time.Now)},
		{writes: true, register: updateDeviceCuration(store)},
	}
}

// addTool is mcpsdk.AddTool with the handler's errors answered the way the
// JSON API answers them: as a problem document, and with the cause of an
// unexpected one kept to the server log.
func addTool[In, Out any](s *mcpsdk.Server, log *slog.Logger, t *mcpsdk.Tool, h mcpsdk.ToolHandlerFor[In, Out]) {
	mcpsdk.AddTool(s, t, func(ctx context.Context, req *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, Out, error) {
		res, out, err := h(ctx, req, in)
		if err != nil {
			return nil, out, toolProblem(ctx, log, t.Name, err)
		}

		return res, out, nil
	})
}

// schemaTypes overrides what schema inference makes of a type whose JSON is
// not its Go shape. An IP address is a struct in Go and a string on the wire,
// and the SDK validates every result against the schema before sending it.
var schemaTypes = map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[netip.Addr](): {Type: "string", Description: "An IPv4 or IPv6 address."},

	// A cursor travels as the opaque token it encodes itself to.
	reflect.TypeFor[inventory.Cursor](): {Type: "string", Description: "An opaque page cursor."},
}

// schemaFor infers the schema of T, a tool's input or output, with the
// overrides in schemaTypes applied.
//
// A tool's types are fixed structs, so a failure here happens on every start
// or never; mcpsdk.AddTool panics on a bad schema for the same reason.
func schemaFor[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T](&jsonschema.ForOptions{TypeSchemas: schemaTypes})
	if err != nil {
		panic(fmt.Sprintf("schema for %s: %v", reflect.TypeFor[T](), err))
	}

	return s
}

// readOnly is the annotation every tool that only reads the inventory carries:
// it changes nothing and reaches nothing outside the recorded inventory.
func readOnly() *mcpsdk.ToolAnnotations {
	return &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)}
}

// enumOf is a set of string constants as a schema's enum values.
func enumOf[T ~string](values []T) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = string(v)
	}

	return out
}
