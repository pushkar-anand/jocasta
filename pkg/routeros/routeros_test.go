package routeros

import (
	"context"
	"encoding/json/v2"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serve builds a client pointed at a test server running handler. It goes
// through New rather than assembling the struct, so the base path, the port
// and the transport are exercised by every test rather than assumed.
func serve(t *testing.T, handler http.HandlerFunc) *RouterOS {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return dial(t, srv.URL, &Config{User: "jocasta", Password: "secret"})
}

// dial points a client at an already-running test server.
func dial(t *testing.T, addr string, cfg *Config) *RouterOS {
	t.Helper()

	u, err := url.Parse(addr)
	require.NoError(t, err)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	cfg.Host = u.Hostname()
	cfg.Port = port

	r, err := New(cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	return r
}

// respond answers every request with body, asserting the path is the console
// path with /rest in front of it.
func respond(t *testing.T, path, body string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, basePath+path, r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func TestNewRefusesAConfigNamingNoRouter(t *testing.T) {
	t.Parallel()

	for name, cfg := range map[string]*Config{
		"nil":       nil,
		"no host":   {User: "jocasta"},
		"port only": {Port: 8080},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := New(cfg, slog.New(slog.DiscardHandler))
			require.ErrorIs(t, err, ErrNoHost)
		})
	}
}

func TestNewBuildsTheBaseURL(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg  Config
		want string
	}{
		"plain defaults to 80":   {Config{Host: "192.0.2.1"}, "http://192.0.2.1:80/rest"},
		"ssl defaults to 443":    {Config{Host: "192.0.2.1", SSL: true}, "https://192.0.2.1:443/rest"},
		"explicit port wins":     {Config{Host: "192.0.2.1", Port: 8080}, "http://192.0.2.1:8080/rest"},
		"explicit port with ssl": {Config{Host: "192.0.2.1", Port: 8443, SSL: true}, "https://192.0.2.1:8443/rest"},
		"name rather than addr":  {Config{Host: "edge.lan"}, "http://edge.lan:80/rest"},
		"ipv6 is bracketed":      {Config{Host: "2001:db8::1"}, "http://[2001:db8::1]:80/rest"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r, err := New(&tc.cfg, slog.New(slog.DiscardHandler))
			require.NoError(t, err)
			assert.Equal(t, tc.want, r.Addr())
		})
	}
}

// New must not touch the network: a router that is down at startup is one to
// retry rather than a reason to refuse to start.
func TestNewPerformsNoIO(t *testing.T) {
	t.Parallel()

	// Port 1 on the discard-only documentation address answers nothing, so a
	// client that dialled here would hang rather than return.
	_, err := New(&Config{Host: "192.0.2.1", Port: 1}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
}

func TestGetSendsCredentialsAndAsksForJSON(t *testing.T) {
	t.Parallel()

	r := serve(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok, "expected basic auth")
		assert.Equal(t, "jocasta", user)
		assert.Equal(t, "secret", pass)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		_, _ = w.Write([]byte(`{"version":"7.14.3 (stable)"}`))
	})

	res, err := r.Resource(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "7.14.3 (stable)", res.Version)
}

// A real /ip/arp table: a reachable host, a stale one, an incomplete entry
// carrying the all-zero address, an invalid row, and a static entry an
// operator typed in.
const arpTable = `[
  {".id":"*1","address":"192.0.2.10","mac-address":"00:00:5E:00:53:01","interface":"bridge","complete":"true","disabled":"false","dynamic":"true","invalid":"false","published":"false","status":"reachable"},
  {".id":"*2","address":"192.0.2.11","mac-address":"00:00:5E:00:53:02","interface":"vlan20","complete":"true","disabled":"false","dynamic":"true","invalid":"false","published":"false","status":"stale"},
  {".id":"*3","address":"192.0.2.12","mac-address":"00:00:00:00:00:00","interface":"vlan20","complete":"false","disabled":"false","dynamic":"true","invalid":"false","published":"false","status":"failed"},
  {".id":"*4","address":"192.0.2.13","mac-address":"00:00:5E:00:53:04","interface":"vlan30","complete":"true","disabled":"false","dynamic":"true","invalid":"true","published":"false"},
  {".id":"*5","address":"192.0.2.14","mac-address":"00:00:5E:00:53:05","interface":"bridge","complete":"true","disabled":"true","dynamic":"false","invalid":"false","published":"false","comment":"printer, static"}
]`

func TestARPDecodesTheTable(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, arpAPI, arpTable))

	entries, err := r.ARP(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 5)

	first := entries[0]
	assert.Equal(t, "*1", first.ID)
	assert.Equal(t, "192.0.2.10", first.Address)
	assert.Equal(t, "00:00:5E:00:53:01", first.MACAddress)
	assert.Equal(t, "bridge", first.Interface)
	assert.Equal(t, "reachable", first.Status)
	assert.True(t, bool(first.Complete))
	assert.True(t, bool(first.Dynamic))
	assert.False(t, bool(first.Invalid))

	// A row that omits a flag means false by that omission.
	assert.Empty(t, entries[3].Status)

	static := entries[4]
	assert.False(t, bool(static.Dynamic))
	assert.Equal(t, "printer, static", static.Comment)
}

func TestARPUsableRejectsTheEntriesThatNameNothing(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, arpAPI, arpTable))

	entries, err := r.ARP(t.Context())
	require.NoError(t, err)

	usable := map[string]bool{}
	for _, e := range entries {
		usable[e.ID] = e.Usable()
	}

	assert.Equal(t, map[string]bool{
		"*1": true,  // reachable
		"*2": true,  // stale, but resolved: the router still knows the address
		"*3": false, // incomplete, hence the all-zero hardware address
		"*4": false, // invalid
		"*5": false, // disabled
	}, usable)
}

// Reachable is the presence test and Usable the identification one: a stale
// entry names a device the router still knows but is not a sighting of it.
func TestARPReachableRejectsWhatTheRouterHasNotHeardFromLately(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, arpAPI, arpTable))

	entries, err := r.ARP(t.Context())
	require.NoError(t, err)

	reachable := map[string]bool{}
	for _, e := range entries {
		reachable[e.ID] = e.Reachable()
	}

	assert.Equal(t, map[string]bool{
		"*1": true,  // reachable
		"*2": false, // stale: resolved, but not heard from lately
		"*3": false, // failed
		"*4": false, // invalid, and no neighbour state to fall back to
		"*5": false, // disabled
	}, reachable)
}

// A RouterOS 6 table carries no neighbour state, and there a resolved entry is
// the strongest signal there is.
func TestARPReachableFallsBackToUsableWithoutNeighbourState(t *testing.T) {
	t.Parallel()

	assert.True(t, ARPEntry{Complete: true}.Reachable())
	assert.False(t, ARPEntry{Complete: true, Invalid: true}.Reachable())
	assert.False(t, ARPEntry{Complete: false}.Reachable())
}

// A real /ip/dhcp-server/lease table: a bound dynamic lease naming itself, a
// bound static lease an operator commented, a static lease for a device that
// is not on the network, and a dynamic lease from a client that sent no name.
const leaseTable = `[
  {".id":"*1","address":"192.0.2.10","active-address":"192.0.2.10","mac-address":"00:00:5E:00:53:01","active-mac-address":"00:00:5E:00:53:01","client-id":"1:0:0:5e:0:53:1","host-name":"laptop","server":"lan","status":"bound","dynamic":"true","blocked":"false","disabled":"false","expires-after":"9m59s","last-seen":"1s"},
  {".id":"*2","address":"192.0.2.11","active-address":"192.0.2.11","mac-address":"00:00:5E:00:53:02","host-name":"ESP-9A2B","comment":"kitchen sensor","server":"iot","status":"bound","dynamic":"false","blocked":"false","disabled":"false","last-seen":"4m12s"},
  {".id":"*3","address":"192.0.2.12","mac-address":"00:00:5E:00:53:03","comment":"garage printer","server":"lan","status":"waiting","dynamic":"false","blocked":"false","disabled":"false","last-seen":"never"},
  {".id":"*4","address":"192.0.2.13","active-address":"192.0.2.13","mac-address":"00:00:5E:00:53:04","host-name":"","server":"lan","status":"bound","dynamic":"true","blocked":"false","disabled":"false","expires-after":"2h","last-seen":"11s"}
]`

func TestDHCPLeasesDecodeTheTable(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, dhcpLeaseAPI, leaseTable))

	leases, err := r.DHCPLeases(t.Context())
	require.NoError(t, err)
	require.Len(t, leases, 4)

	dynamic := leases[0]
	assert.Equal(t, "192.0.2.10", dynamic.Address)
	assert.Equal(t, "00:00:5E:00:53:01", dynamic.MACAddress)
	assert.Equal(t, "laptop", dynamic.HostName)
	assert.Equal(t, "lan", dynamic.Server)
	assert.Equal(t, "9m59s", dynamic.ExpiresAfter)
	assert.Equal(t, "1s", dynamic.LastSeen)

	// A static lease no client is using carries the configured address and no
	// active one.
	assert.Equal(t, "192.0.2.12", leases[2].Address)
	assert.Empty(t, leases[2].ActiveAddress)

	// The two are kept apart: the client's claim about its own name, and the
	// operator's note about the lease.
	assert.Equal(t, "ESP-9A2B", leases[1].HostName)
	assert.Equal(t, "kitchen sensor", leases[1].Comment)
}

func TestDHCPLeaseReadsStandingAndPresenceApart(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, dhcpLeaseAPI, leaseTable))

	leases, err := r.DHCPLeases(t.Context())
	require.NoError(t, err)

	type reading struct {
		static bool
		bound  bool
	}

	got := map[string]reading{}
	for _, l := range leases {
		got[l.ID] = reading{l.Static(), l.Bound()}
	}

	assert.Equal(t, map[string]reading{
		// Handed out by the server, and the client is holding it.
		"*1": {false, true},
		// Configured by an operator, and in use.
		"*2": {true, true},
		// Configuration for a device nobody has plugged in: the lease exists
		// and is no evidence that the device does.
		"*3": {true, false},
		"*4": {false, true},
	}, got)
}

func TestAddressesDecodeTheTable(t *testing.T) {
	t.Parallel()

	body := `[{".id":"*1","address":"192.0.2.1/24","network":"192.0.2.0",` +
		`"interface":"vlan10","actual-interface":"vlan10","dynamic":"false",` +
		`"disabled":"false","invalid":"false","comment":"Home"}]`

	out, err := serve(t, respond(t, addressAPI, body)).Addresses(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Equal(t, "192.0.2.1/24", out[0].Address)
	assert.Equal(t, "192.0.2.0", out[0].Network)
	assert.Equal(t, "vlan10", out[0].Interface)
	assert.Equal(t, "Home", out[0].Comment)
	assert.True(t, out[0].Usable())
}

// The WAN address the ISP hands out is dynamic, and a link the router sits on
// is not a segment it serves.
func TestAddressUsableRejectsWhatIsNotASegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr IPAddress
		want bool
	}{
		{"configured", IPAddress{Address: "192.0.2.1/24"}, true},
		{"handed to the router", IPAddress{Address: "203.0.113.7/24", Dynamic: true}, false},
		{"turned off", IPAddress{Address: "192.0.2.1/24", Disabled: true}, false},
		{"interface is gone", IPAddress{Address: "192.0.2.1/24", Invalid: true}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.addr.Usable())
		})
	}
}

func TestVLANsDecodeTheTable(t *testing.T) {
	t.Parallel()

	body := `[{".id":"*1","name":"vlan10","vlan-id":"10","interface":"bridge",` +
		`"disabled":"false","running":"true","comment":"Home"}]`

	out, err := serve(t, respond(t, vlanAPI, body)).VLANs(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Equal(t, "vlan10", out[0].Name)
	assert.Equal(t, "bridge", out[0].Interface)
	assert.Equal(t, "Home", out[0].Comment)
	assert.True(t, bool(out[0].Running))

	tag, ok := out[0].Tag()
	require.True(t, ok)
	assert.Equal(t, 10, tag)
}

// A row whose tag does not read as a number names no segment, and reporting it
// as VLAN 0 would claim the segment is untagged.
func TestVLANTagRefusesWhatIsNotANumber(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "none", "10-20"} {
		tag, ok := VLAN{VLANID: raw}.Tag()

		assert.False(t, ok, raw)
		assert.Zero(t, tag)
	}
}

func TestEmptyTableIsNotAnError(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, arpAPI, `[]`))

	entries, err := r.ARP(t.Context())
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestVerifyReportsWhatTheRouterIs(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, resourceAPI, `{"architecture-name":"arm","board-name":"RB5009UG+S+","cpu":"ARM64","free-memory":"850841600","platform":"MikroTik","total-memory":"1073741824","uptime":"1w2d3h4m5s","version":"7.14.3 (stable)"}`))

	res, err := r.Verify(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "RB5009UG+S+", res.BoardName)
	assert.Equal(t, "7.14.3 (stable)", res.Version)
	assert.Equal(t, "MikroTik", res.Platform)
	assert.Equal(t, "1073741824", res.TotalMemory)
}

func TestRejectedCredentialsAreNotWorthRetrying(t *testing.T) {
	t.Parallel()

	r := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":401,"message":"Unauthorized"}`))
	})

	_, err := r.Verify(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)
	assert.NotErrorIs(t, err, ErrUnreachable)
	assert.Contains(t, err.Error(), "Unauthorized")

	var rErr *Error

	require.ErrorAs(t, err, &rErr)
	assert.Equal(t, http.StatusUnauthorized, rErr.Status)
	assert.Equal(t, 401, rErr.Code)
}

func TestTheRouterSaysWhyItRefused(t *testing.T) {
	t.Parallel()

	r := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":403,"message":"Forbidden","detail":"not enough permissions (9)"}`))
	})

	_, err := r.ARP(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)
	assert.Contains(t, err.Error(), "not enough permissions")
}

// A user that logs in and is then refused every command is a credentials
// problem wearing a 500. Captured from a real router whose API user had the
// rest-api policy and not read.
func TestAUserWithoutThePolicyIsARefusal(t *testing.T) {
	t.Parallel()

	r := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":500,"message":"Internal Server Error","detail":"std failure: not allowed (9)"}`))
	})

	_, err := r.ARP(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)
	assert.NotErrorIs(t, err, ErrUnreachable)
	assert.Contains(t, err.Error(), "not allowed")
}

// Every other fault the router reports is left alone: a 500 is not evidence
// about the credentials unless the router says the command was not allowed.
func TestOtherFaultsAreNotReadAsARefusal(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"another std failure": `{"error":500,"message":"Internal Server Error","detail":"std failure: item not found (2)"}`,
		"no detail":           `{"error":500,"message":"Internal Server Error"}`,
		"no code":             `{"error":500,"message":"Internal Server Error","detail":"not allowed"}`,
		"unparseable code":    `{"error":500,"message":"Internal Server Error","detail":"not allowed (nine)"}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(body))
			})

			_, err := r.ARP(t.Context())
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrUnauthorized)
		})
	}
}

func TestMissingEndpointIsToldApartFromARefusal(t *testing.T) {
	t.Parallel()

	r := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":404,"message":"Not Found","detail":"no such command prefix"}`))
	})

	_, err := r.DHCPLeases(t.Context())
	require.ErrorIs(t, err, ErrNotFound)
	assert.NotErrorIs(t, err, ErrUnauthorized)
}

// Something answering this port that is not a router describes itself in its
// own terms, and the status is still the fact.
func TestANonRouterErrorBodyStillCarriesTheStatus(t *testing.T) {
	t.Parallel()

	r := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	})

	_, err := r.ARP(t.Context())
	require.Error(t, err)

	var rErr *Error

	require.ErrorAs(t, err, &rErr)
	assert.Equal(t, http.StatusBadGateway, rErr.Status)
	assert.Contains(t, err.Error(), "502")
}

func TestARouterThatDoesNotAnswerIsWorthRetrying(t *testing.T) {
	t.Parallel()

	// A listener closed the moment its address is known leaves a port nothing
	// is on, which is what a firewalled or powered-off router looks like.
	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := "http://" + l.Addr().String()

	require.NoError(t, l.Close())

	r := dial(t, addr, &Config{})

	_, err = r.Verify(t.Context())
	require.ErrorIs(t, err, ErrUnreachable)
	assert.NotErrorIs(t, err, ErrUnauthorized)
}

// A caller giving up is not the router being absent: calling it unreachable
// would have a poller retry all the way through a shutdown.
func TestACancelledContextIsNotAnUnreachableRouter(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, arpAPI, `[]`))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := r.ARP(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestABodyThatIsNotJSONIsAnError(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, arpAPI, `not-json`))

	_, err := r.ARP(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), arpAPI)
}

// RouterOS generates its own certificate for www-ssl unless one is imported,
// so the ordinary setup only connects with verification off.
func TestInsecureAcceptsTheRoutersOwnCertificate(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(respond(t, resourceAPI, `{"version":"7.14.3 (stable)"}`))
	t.Cleanup(srv.Close)

	t.Run("verified", func(t *testing.T) {
		t.Parallel()

		r := dial(t, srv.URL, &Config{SSL: true})

		_, err := r.Verify(t.Context())
		require.ErrorIs(t, err, ErrTLS)
		assert.NotErrorIs(t, err, ErrUnreachable)
	})

	t.Run("skipped", func(t *testing.T) {
		t.Parallel()

		r := dial(t, srv.URL, &Config{SSL: true, Insecure: true})

		res, err := r.Verify(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "7.14.3 (stable)", res.Version)
	})
}

func TestBoolAbsorbsEveryRenderingTheRouterUses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		json string
		want bool
	}{
		"string true":  {`{"flag":"true"}`, true},
		"string false": {`{"flag":"false"}`, false},
		"bare true":    {`{"flag":true}`, true},
		"bare false":   {`{"flag":false}`, false},
		"empty":        {`{"flag":""}`, false},
		"null":         {`{"flag":null}`, false},
		"absent":       {`{}`, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var row struct {
				Flag Bool `json:"flag"`
			}

			require.NoError(t, json.Unmarshal([]byte(tc.json), &row))
			assert.Equal(t, tc.want, bool(row.Flag))
		})
	}
}

func TestBoolRefusesAValueThatIsNotABoolean(t *testing.T) {
	t.Parallel()

	var row struct {
		Flag Bool `json:"flag"`
	}

	require.Error(t, json.Unmarshal([]byte(`{"flag":"maybe"}`), &row))
}

func TestBoolRendersTheWayTheRouterDoes(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(struct {
		Flag Bool `json:"flag"`
	}{Flag: true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"flag":"true"}`, string(b))
}
