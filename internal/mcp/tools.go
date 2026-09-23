package mcp

import (
	"fmt"
	"net/netip"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// tool is one entry in what the server offers.
type tool struct {
	// writes marks a tool that changes the inventory. Only a read_write token
	// is shown one.
	writes bool

	// register adds the tool to a server. It is called once per server the
	// tool belongs on.
	register func(*mcpsdk.Server)
}

// tools is every tool the server offers. A new tool is a file defining it and
// one line here.
func tools(store *inventory.Store) []tool {
	return []tool{
		{register: listDevices(store)},
	}
}

// schemaTypes overrides what schema inference makes of a type whose JSON is
// not its Go shape. An IP address is a struct in Go and a string on the wire,
// and the SDK validates every result against the schema before sending it.
var schemaTypes = map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[netip.Addr](): {Type: "string", Description: "An IPv4 or IPv6 address."},
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
