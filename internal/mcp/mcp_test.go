package mcp_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/api"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	jocastamcp "github.com/pushkar-anand/jocasta/internal/mcp"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	server *httptest.Server
	rest   http.Handler
	auth   *auth.Auth
	store  *inventory.Store
	read   string
	write  string
	readID int64
	userID int64
}

func setup(t *testing.T) *fixture {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	store := inventory.New(conn, log)
	host, err := hosts.BuildHost(t.Context(), hosts.HostInput{IP: "192.0.2.10", MAC: "00:00:5e:00:53:01", Hostname: "printer.local"})
	require.NoError(t, err)
	_, err = store.RecordSweep(t.Context(), "fixture", netip.MustParsePrefix("192.0.2.0/24"), []scanner.Host{{Host: host}})
	require.NoError(t, err)
	require.NoError(t, store.RecordNetworks(t.Context(), []plugin.Network{
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Name: "Home", VLAN: 10},
		{Prefix: netip.MustParsePrefix("198.51.100.0/24"), Name: "Empty", VLAN: 20},
	}))

	for _, ports := range [][]uint16{{22, 443}, {443}} {
		_, err = store.RecordPorts(t.Context(), "fixture", []scanner.PortScan{{Addr: netip.MustParseAddr("192.0.2.10"), Open: ports, Scanned: []uint16{22, 443}}})
		require.NoError(t, err)
	}

	q := models.New(conn)
	user, err := q.CreateUser(t.Context(), models.CreateUserParams{Username: "fixture", PasswordHash: "unused", Role: dbtype.RoleAdmin})
	require.NoError(t, err)
	a, err := auth.New(q, password.NewHasher())
	require.NoError(t, err)
	read, token, err := a.CreateToken(t.Context(), user.ID, "read", dbtype.TokenRead)
	require.NoError(t, err)
	write, _, err := a.CreateToken(t.Context(), user.ID, "write", dbtype.TokenReadWrite)
	require.NoError(t, err)
	v, err := validator.New(validator.WithCustomTags(map[string]validator.ValidationFunc{"deviceclass": api.DeviceClassRule}))
	require.NoError(t, err)

	server := httptest.NewServer(jocastamcp.NewHandler(store, a, v, log))
	t.Cleanup(server.Close)

	jw := response.NewJSONWriter(log, response.WithErrorProblemMapper(func(err error) response.Problem {
		if errors.Is(err, inventory.ErrNotFound) {
			return response.NewProblem().WithStatus(http.StatusNotFound).Build()
		}

		return nil
	}))
	rest := api.NewHandler(log, request.NewReader(log, v, request.WithRejectUnknownFields()), store, jw)

	return &fixture{server: server, rest: rest, auth: a, store: store, read: read, write: write, readID: token.ID, userID: user.ID}
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	cloned := r.Clone(r.Context())
	cloned.Header.Set("Authorization", "Bearer "+b.token)

	return http.DefaultTransport.RoundTrip(cloned)
}

func connect(t *testing.T, f *fixture, token string) *sdk.ClientSession {
	t.Helper()

	client := sdk.NewClient(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &sdk.StreamableClientTransport{
		Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: token}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session
}

func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)

	return result
}

func TestReadToolsMatchREST(t *testing.T) {
	t.Parallel()
	f := setup(t)
	session := connect(t, f, f.read)
	tools, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 13)

	for _, tool := range tools.Tools {
		assert.Equal(t, tool.Name != "update_device_curation", tool.Annotations.ReadOnlyHint)
	}

	tests := []struct {
		tool string
		path string
		args map[string]any
	}{
		{"get_stats", "/stats", nil},
		{"list_groups", "/groups", nil},
		{"list_devices", "/devices?network_id=1", map[string]any{"network_id": 1}},
		{"get_device", "/devices/1", map[string]any{"id": 1}},
		{"list_networks", "/networks", nil},
		{"get_network", "/networks/1", map[string]any{"id": 1}},
		{"get_device_ports", "/devices/1/ports?state=closed", map[string]any{"id": 1, "state": "closed"}},
		{"get_port_overview", "/ports/overview?service_limit=1", map[string]any{"service_limit": 1}},
		{"get_device_sources", "/devices/1/sources", map[string]any{"id": 1}},
		{"get_device_events", "/devices/1/events?limit=1", map[string]any{"id": 1, "limit": 1}},
		{"list_events", "/events?event_kinds=PORT_OPENED&event_kinds=PORT_CLOSED", map[string]any{"event_kinds": []string{"PORT_OPENED", "PORT_CLOSED"}}},
		{"list_scans", "/scans?kind=PORTS&limit=1", map[string]any{"kind": "PORTS", "limit": 1}},
	}
	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			args := tc.args
			if args == nil {
				args = map[string]any{}
			}

			result := call(t, session, tc.tool, args)
			require.False(t, result.IsError, "%v", result.Content)

			rec := httptest.NewRecorder()
			f.rest.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.NotEmpty(t, result.Content)
			content, ok := result.Content[0].(*sdk.TextContent)
			require.True(t, ok)
			assert.JSONEq(t, rec.Body.String(), content.Text)

			structured, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			assert.JSONEq(t, content.Text, string(structured))
		})
	}
}

func TestCurationPermissionsAndRequiredFields(t *testing.T) {
	t.Parallel()
	f := setup(t)
	read := connect(t, f, f.read)
	write := connect(t, f, f.write)
	args := map[string]any{"id": 1, "label": "Office printer", "group": "Office", "type": "printer", "notes": "Upstairs", "ignored": false}
	assert.True(t, call(t, read, "update_device_curation", args).IsError)
	device, err := f.store.Device(t.Context(), 1)
	require.NoError(t, err)
	assert.Empty(t, device.Label)

	for _, field := range []string{"id", "label", "group", "type", "notes", "ignored"} {
		missing := make(map[string]any, len(args))
		for k, v := range args {
			if k != field {
				missing[k] = v
			}
		}

		result, err := write.CallTool(t.Context(), &sdk.CallToolParams{Name: "update_device_curation", Arguments: missing})
		assert.True(t, err != nil || result.IsError, "missing %s must fail", field)
	}

	for field, limit := range map[string]int{"label": 200, "group": 100, "notes": 2000} {
		original := args[field]
		args[field] = strings.Repeat("x", limit+1)
		assert.True(t, call(t, write, "update_device_curation", args).IsError)
		args[field] = original
	}

	require.False(t, call(t, write, "update_device_curation", args).IsError)
	device, err = f.store.Device(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, "Office printer", device.Label)
	assert.Equal(t, "Upstairs", device.Notes)
	events, err := f.store.DeviceEvents(t.Context(), 1, 50)
	require.NoError(t, err)

	found := false
	for _, event := range events {
		found = found || event.Kind == dbtype.EventDeviceEdited
	}

	assert.True(t, found)
	require.NoError(t, f.auth.RevokeToken(t.Context(), f.userID, f.readID))
	_, err = read.CallTool(t.Context(), &sdk.CallToolParams{Name: "get_stats", Arguments: map[string]any{}})
	require.Error(t, err)
}

func TestValidationAndMissingRecords(t *testing.T) {
	t.Parallel()
	f := setup(t)

	session := connect(t, f, f.read)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"get_device", map[string]any{"id": 0}},
		{"get_device_ports", map[string]any{"id": 999}},
		{"get_device_sources", map[string]any{"id": 999}},
		{"get_network", map[string]any{"id": 999}},
		{"list_devices", map[string]any{"type": "hovercraft"}},
		{"list_events", map[string]any{"cursor": "!!!"}},
		{"list_events", map[string]any{"event_kinds": []string{"INVALID"}}},
		{"list_scans", map[string]any{"kind": "INVALID"}},
		{"list_scans", map[string]any{"limit": 501}},
		{"get_port_overview", map[string]any{"service_limit": -1}},
	} {
		t.Run(tc.tool, func(t *testing.T) { assert.True(t, call(t, session, tc.tool, tc.args).IsError) })
	}
}

func TestHTTPAuthenticationAndOrigin(t *testing.T) {
	t.Parallel()

	f := setup(t)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		for _, tc := range []struct {
			token, origin string
			status        int
		}{
			{"", "", http.StatusUnauthorized},
			{"invalid", "", http.StatusUnauthorized},
			{f.read, "https://attacker.example", http.StatusForbidden},
			{f.read, "null", http.StatusForbidden},
		} {
			req := httptest.NewRequestWithContext(t.Context(), method, f.server.URL+"/mcp", nil)
			req.RequestURI = ""
			req.Header.Set("Origin", tc.origin)

			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}

			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			assert.Equal(t, tc.status, res.StatusCode)
			assert.Empty(t, res.Header.Get("Location"))
		}
	}
}

func TestDeviceHistoryPagination(t *testing.T) {
	t.Parallel()
	f := setup(t)
	session := connect(t, f, f.read)

	var cursor string

	seen := map[float64]bool{}

	for range 30 {
		args := map[string]any{"id": 1, "limit": 1, "cursor": cursor}
		result := call(t, session, "get_device_events", args)
		require.False(t, result.IsError)

		var page struct {
			Events []map[string]any `json:"events"`
			Next   string           `json:"next_cursor"`
		}
		require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*sdk.TextContent).Text), &page))

		for _, event := range page.Events {
			id := event["id"].(float64)
			require.False(t, seen[id])
			seen[id] = true
		}

		if page.Next == "" {
			break
		}

		cursor = page.Next
		rec := httptest.NewRecorder()
		f.rest.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/devices/1/events?limit=1&cursor="+url.QueryEscape(cursor), nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	events, err := f.store.DeviceEvents(t.Context(), 1, 100)
	require.NoError(t, err)
	assert.Len(t, seen, len(events))
}

func TestNetworkMembershipAndIgnoredPorts(t *testing.T) {
	t.Parallel()
	f := setup(t)
	session := connect(t, f, f.read)
	host, err := hosts.BuildHost(t.Context(), hosts.HostInput{IP: "198.51.100.10", MAC: "00:00:5e:00:53:01"})
	require.NoError(t, err)
	_, err = f.store.RecordSweep(t.Context(), "second-segment", netip.MustParsePrefix("198.51.100.0/24"), []scanner.Host{{Host: host}})
	require.NoError(t, err)
	networks, err := f.store.ListNetworks(t.Context())
	require.NoError(t, err)

	var networkID int64

	for _, network := range networks {
		if network.CIDR == "198.51.100.0/24" {
			networkID = network.ID
		}
	}

	require.Positive(t, networkID)
	result := call(t, session, "list_devices", map[string]any{"network_id": networkID})
	require.False(t, result.IsError)

	var devices struct {
		Devices []inventory.Device `json:"devices"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*sdk.TextContent).Text), &devices))
	require.Len(t, devices.Devices, 1)
	assert.Len(t, devices.Devices[0].Networks, 2)
	assert.Equal(t, []uint16{443}, devices.Devices[0].OpenPorts)
	_, err = f.store.UpdateCuration(t.Context(), 1, inventory.Curation{Ignored: true})
	require.NoError(t, err)
	result = call(t, session, "list_devices", map[string]any{})
	require.False(t, result.IsError)
	assert.JSONEq(t, `{"devices":[],"count":0}`, result.Content[0].(*sdk.TextContent).Text)
	result = call(t, session, "get_port_overview", map[string]any{})
	require.False(t, result.IsError)

	var overview inventory.PortOverview
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*sdk.TextContent).Text), &overview))
	assert.Zero(t, overview.Open)
	assert.Empty(t, overview.Services)
	result = call(t, session, "list_devices", map[string]any{"include_ignored": true})
	require.False(t, result.IsError)
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*sdk.TextContent).Text), &devices))
	assert.Len(t, devices.Devices, 1)
	result = call(t, session, "list_events", map[string]any{"exclude_ignored": true})
	require.False(t, result.IsError)
	assert.JSONEq(t, `{"events":[],"count":0}`, result.Content[0].(*sdk.TextContent).Text)
}
