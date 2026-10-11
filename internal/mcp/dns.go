package mcp

import (
	"cmp"
	"context"
	"log/slog"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// listDNSInput names the device and the period.
type listDNSInput struct {
	DeviceID int64 `json:"device_id" jsonschema:"The device, from get_device or list_devices."`
	Days     int   `json:"days,omitempty" jsonschema:"How many days back to cover. Defaults to 1."`
	Limit    int   `json:"limit,omitempty" jsonschema:"Most names to return, busiest first. Defaults to 50."`
}

// listDNSOutput is the names one device looked up.
type listDNSOutput struct {
	// Recorded is false when no resolver's log has ever been read, meaning
	// nothing is collecting lookups. A device that looked nothing up reports
	// true.
	Recorded bool                 `json:"recorded"`
	Since    time.Time            `json:"since"`
	Names    []*inventory.DNSName `json:"names"`
}

// listDNS offers one device's lookups.
func listDNS(store *inventory.Store, now func() time.Time) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "list_dns",
		Title: "List DNS lookups",
		Description: "The names one device looked up, from hourly counts of the query log of the DNS resolver Jocasta reads, busiest first. " +
			"Each name says how many lookups there were, how many the resolver blocked (a blocklist entry, such as an ad or tracking domain), " +
			"how many failed (the name does not exist, or the upstream server failed), and the latest hour it was looked up in. " +
			"Use it to say which services a device uses or what a blocklist stopped; list_traffic says which addresses it exchanged data with. " +
			"A device that uses another resolver, or whose lookups the router forwards, has none recorded. " +
			"recorded false means no resolver is read at all.",
		InputSchema:  listDNSSchema(),
		OutputSchema: schemaFor[listDNSOutput](),
		Annotations:  readOnly(),
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in listDNSInput,
	) (*mcpsdk.CallToolResult, listDNSOutput, error) {
		days := cmp.Or(in.Days, trafficDays)
		limit := cmp.Or(in.Limit, inventory.DefaultPageSize)
		since := now().Add(-time.Duration(days) * 24 * time.Hour)

		// An id that names no device is a 404.
		if err := store.RequireDevice(ctx, in.DeviceID); err != nil {
			return nil, listDNSOutput{}, err
		}

		recorded, err := store.DNSRead(ctx)
		if err != nil {
			return nil, listDNSOutput{}, err
		}

		names, err := store.DeviceDNS(ctx, in.DeviceID, since)
		if err != nil {
			return nil, listDNSOutput{}, err
		}

		return nil, listDNSOutput{
			Recorded: recorded,
			Since:    since.UTC().Truncate(time.Hour),
			Names:    names[:min(len(names), limit)],
		}, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// listDNSSchema is the schema inferred from listDNSInput, with its bounds.
func listDNSSchema() *jsonschema.Schema {
	s := schemaFor[listDNSInput]()

	s.Properties["device_id"].Minimum = new(1.0)
	s.Properties["days"].Minimum = new(1.0)
	s.Properties["days"].Maximum = new(float64(trafficMaxDays))
	s.Properties["limit"].Minimum = new(1.0)
	s.Properties["limit"].Maximum = new(float64(inventory.MaxPageSize))

	return s
}
