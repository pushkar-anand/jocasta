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

// The period list_traffic covers when none is asked for, and the longest it
// may. Traffic retention is what really bounds how far back it can reach.
const (
	trafficDays    = 1
	trafficMaxDays = 90
)

// Traffic scopes, for a device's view: everything, only peers on the local
// network, or only internet organisations.
const (
	scopeAll      = "all"
	scopeLocal    = "local"
	scopeInternet = "internet"
)

// listTrafficInput picks one of three views: one device's peers when
// device_id is set, the network's first contacts when first_contact_only is,
// and otherwise a network summary.
type listTrafficInput struct {
	DeviceID         int64  `json:"device_id,omitempty" jsonschema:"Only this device's traffic, from get_device or list_devices. Omit for the whole network."`
	Days             int    `json:"days,omitempty" jsonschema:"How many days back to cover. Defaults to 1."`
	Scope            string `json:"scope,omitempty" jsonschema:"For one device: all peers, only local ones, or only internet organisations. Defaults to all."`
	FirstContactOnly bool   `json:"first_contact_only,omitempty" jsonschema:"Only organisations a device exchanged data with for the first time within days. Combine with device_id to ask about one device."`
	Group            string `json:"group,omitempty" jsonschema:"Only devices in this group, from list_groups. Applies to the network summary and first contacts."`
	Limit            int    `json:"limit,omitempty" jsonschema:"Most rows in each list. Defaults to 50."`
}

// listTrafficOutput carries whichever view was asked for; the others are
// absent.
type listTrafficOutput struct {
	// Recorded is false when no traffic has ever been recorded: nothing is
	// collecting, which is different from a quiet network.
	Recorded bool      `json:"recorded"`
	Since    time.Time `json:"since"`

	Device *inventory.DeviceTraffic `json:"device,omitempty"`

	BusiestDevices []*inventory.DeviceTotal `json:"busiest_devices,omitempty"`
	Organisations  []*inventory.OrgTotal    `json:"organisations,omitempty"`

	FirstContacts *inventory.FirstContacts `json:"first_contacts,omitempty"`
}

// listTraffic offers the traffic views as one tool.
func listTraffic(store *inventory.Store, now func() time.Time) func(*mcpsdk.Server, *slog.Logger) {
	t := &mcpsdk.Tool{
		Name:  "list_traffic",
		Title: "List traffic",
		Description: "Who devices exchanged data with, from hourly totals the router exported. " +
			"With device_id: that device's peers, split into local ones (other devices, linked by id, and local addresses no device holds) " +
			"and internet ones grouped by the organisation (autonomous system) announcing each address, with bytes sent and received each way. " +
			"With first_contact_only: organisations a device exchanged data with for the first time within the period; " +
			"first_contacts.partial says records do not reach back that far yet, so everything looks new. " +
			"With neither: the busiest devices and the organisations the whole network exchanged the most with. " +
			"Totals are per hour, not individual connections, and cover only what the router exported. " +
			"recorded false means nothing is collecting traffic, not that the network is quiet.",
		InputSchema:  listTrafficSchema(),
		OutputSchema: schemaFor[listTrafficOutput](),
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: new(false),
		},
	}

	handler := func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		in listTrafficInput,
	) (*mcpsdk.CallToolResult, listTrafficOutput, error) {
		days := cmp.Or(in.Days, trafficDays)
		limit := cmp.Or(in.Limit, pageSize)
		since := now().Add(-time.Duration(days) * 24 * time.Hour)

		recorded, err := store.TrafficRecorded(ctx)
		if err != nil {
			return nil, listTrafficOutput{}, err
		}

		out := listTrafficOutput{Recorded: recorded, Since: since.UTC().Truncate(time.Hour)}

		if in.DeviceID != 0 {
			// An id that names no device is a 404, not an empty answer.
			if _, err := store.Device(ctx, in.DeviceID); err != nil {
				return nil, listTrafficOutput{}, err
			}
		}

		switch {
		case in.FirstContactOnly:
			fc, err := store.FirstContacts(ctx, since, in.Group, limit)
			if err != nil {
				return nil, listTrafficOutput{}, err
			}

			if in.DeviceID != 0 {
				kept := fc.Contacts[:0]

				for _, c := range fc.Contacts {
					if c.DeviceID == in.DeviceID {
						kept = append(kept, c)
					}
				}

				fc.Contacts = kept
			}

			out.FirstContacts = fc

		case in.DeviceID != 0:
			dt, err := store.DeviceTraffic(ctx, in.DeviceID, since)
			if err != nil {
				return nil, listTrafficOutput{}, err
			}

			out.Device = scoped(dt, cmp.Or(in.Scope, scopeAll), limit)

		default:
			if out.BusiestDevices, err = store.BusiestDevices(ctx, since, in.Group, limit); err != nil {
				return nil, listTrafficOutput{}, err
			}

			if out.Organisations, err = store.TopOrganisations(ctx, since, in.Group, limit); err != nil {
				return nil, listTrafficOutput{}, err
			}
		}

		return nil, out, nil
	}

	return func(s *mcpsdk.Server, log *slog.Logger) { addTool(s, log, t, handler) }
}

// scoped trims a device's traffic to the scope and limit asked for. Lists stay
// empty rather than absent, so an agent reads "none" and not "not asked".
func scoped(dt *inventory.DeviceTraffic, scope string, limit int) *inventory.DeviceTraffic {
	switch scope {
	case scopeLocal:
		dt.Internet = []*inventory.TrafficOrg{}
	case scopeInternet:
		dt.Local = []*inventory.TrafficPeer{}
	}

	dt.Local = dt.Local[:min(len(dt.Local), limit)]
	dt.Internet = dt.Internet[:min(len(dt.Internet), limit)]

	return dt
}

// listTrafficSchema is the schema inferred from listTrafficInput, with its
// bounds and the scopes spelled out.
func listTrafficSchema() *jsonschema.Schema {
	s := schemaFor[listTrafficInput]()

	s.Properties["device_id"].Minimum = new(1.0)
	s.Properties["days"].Minimum = new(1.0)
	s.Properties["days"].Maximum = new(float64(trafficMaxDays))
	s.Properties["limit"].Minimum = new(1.0)
	s.Properties["limit"].Maximum = new(float64(pageLimit))
	s.Properties["scope"].Enum = []any{scopeAll, scopeLocal, scopeInternet}

	return s
}
