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

// tool is one entry of the server.
type tool struct {
	// writes marks a tool that changes the inventory. Only a read_write token
	// sees such a tool.
	writes bool

	// register adds the tool to a server, usually through addTool. It runs
	// once for each server of the tool.
	register func(*mcpsdk.Server, *slog.Logger)
}

// tools is every tool of the server. A new tool needs a file that defines it,
// and one line here.
func tools(store *inventory.Store) []tool {
	return []tool{
		{register: listDevices(store)},
		{register: getDevice(store)},
		{register: explainDevice(store)},
		{register: listEvents(store)},
		{register: listNetworks(store)},
		{register: getNetwork(store)},
		{register: getStats(store)},
		{register: listGroups(store)},
		{register: getPortOverview(store)},
		{register: listScans(store)},
		{register: listTraffic(store, time.Now)},
		{register: listDNS(store, time.Now)},
		{register: getTopology(store)},
		{writes: true, register: updateDeviceCuration(store)},
		{writes: true, register: watchDevice(store)},
	}
}

// addTool is mcpsdk.AddTool, with the errors of the handler answered as the
// JSON API answers them. The answer is a problem document. The cause of an
// unexpected error stays in the server log.
func addTool[In, Out any](s *mcpsdk.Server, log *slog.Logger, t *mcpsdk.Tool, h mcpsdk.ToolHandlerFor[In, Out]) {
	mcpsdk.AddTool(s, t, func(ctx context.Context, req *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, Out, error) {
		res, out, err := h(ctx, req, in)
		if err != nil {
			return nil, out, toolProblem(ctx, log, t.Name, err)
		}

		return res, out, nil
	})
}

// schemaTypes overrides the schema inference for a type whose JSON shape is not
// its Go shape. An IP address is a struct in Go and a string in JSON. The SDK
// validates every result against the schema before it sends it.
var schemaTypes = map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[netip.Addr](): {Type: "string", Description: "An IPv4 or IPv6 address."},

	// A cursor goes as the opaque token that it encodes itself to.
	reflect.TypeFor[inventory.Cursor](): {Type: "string", Description: "An opaque page cursor."},
}

// schemaFor infers the schema of T, the input or output of a tool. It applies
// the overrides in schemaTypes.
//
// The types of a tool are fixed structs. So a failure here happens on every
// start, or never. For the same reason, mcpsdk.AddTool panics on a bad
// schema.
func schemaFor[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T](&jsonschema.ForOptions{TypeSchemas: schemaTypes})
	if err != nil {
		panic(fmt.Sprintf("schema for %s: %v", reflect.TypeFor[T](), err))
	}

	return s
}

// readOnly is the annotation of every tool that only reads the inventory. The
// tool changes nothing, and reaches nothing outside the recorded inventory.
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
