package scanner

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deviceDescription returns a UPnP description in the shape a TV sends, whose
// root device is named friendlyName.
func deviceDescription(friendlyName string) string {
	return `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
    <friendlyName>` + friendlyName + `</friendlyName>
    <manufacturer>Example</manufacturer>
  </device>
</root>`
}

// ssdpReply answers an SSDP search with a response pointing at location.
func ssdpReply(location string) func([]byte) ([]byte, bool) {
	return func(query []byte) ([]byte, bool) {
		if !strings.HasPrefix(string(query), "M-SEARCH * HTTP/1.1\r\n") {
			return nil, false
		}

		return []byte("HTTP/1.1 200 OK\r\n" +
			"CACHE-CONTROL: max-age=1800\r\n" +
			"EXT:\r\n" +
			"LOCATION: " + location + "\r\n" +
			"ST: upnp:rootdevice\r\n" +
			"USN: uuid:00000000-0000-0000-0000-000000000001::upnp:rootdevice\r\n" +
			"\r\n"), true
	}
}

// descriptionServer serves body at /description.xml on a loopback address.
func descriptionServer(t *testing.T, body string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/description.xml" {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestParseSearchResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		msg    string
		want   string
		wantOK bool
	}{
		{
			name:   "a 200 with a LOCATION",
			msg:    "HTTP/1.1 200 OK\r\nLOCATION: http://192.0.2.10:8080/description.xml\r\n\r\n",
			want:   "http://192.0.2.10:8080/description.xml",
			wantOK: true,
		},
		{
			name:   "the header in lower case",
			msg:    "HTTP/1.1 200 OK\r\nlocation: http://192.0.2.10/d.xml\r\n\r\n",
			want:   "http://192.0.2.10/d.xml",
			wantOK: true,
		},
		{name: "no LOCATION", msg: "HTTP/1.1 200 OK\r\nST: upnp:rootdevice\r\n\r\n"},
		{name: "a status other than 200", msg: "HTTP/1.1 404 Not Found\r\nLOCATION: http://192.0.2.10/d.xml\r\n\r\n"},
		{name: "another device's search", msg: string(ssdpSearch)},
		{name: "an announcement", msg: "NOTIFY * HTTP/1.1\r\nLOCATION: http://192.0.2.10/d.xml\r\n\r\n"},
		{name: "not HTTP", msg: "\x00\x01\x02"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseSearchResponse([]byte(tt.msg))

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDescriptionURL(t *testing.T) {
	t.Parallel()

	from := netip.MustParseAddr("192.0.2.10")

	tests := []struct {
		name   string
		loc    string
		wantOK bool
	}{
		{name: "http on the address that answered", loc: "http://192.0.2.10:49152/description.xml", wantOK: true},
		{name: "http on the default port", loc: "http://192.0.2.10/description.xml", wantOK: true},
		{name: "another address", loc: "http://192.0.2.11/description.xml"},
		{name: "a host name", loc: "http://tv.local/description.xml"},
		{name: "https", loc: "https://192.0.2.10/description.xml"},
		{name: "another scheme", loc: "file:///etc/passwd"},
		{name: "user info", loc: "http://admin@192.0.2.10/description.xml"},
		{name: "a relative URL", loc: "/description.xml"},
		{name: "something unparsable", loc: "http://[::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u, ok := descriptionURL(from, tt.loc)

			assert.Equal(t, tt.wantOK, ok)

			if tt.wantOK {
				assert.Equal(t, tt.loc, u.String())
			}
		})
	}
}

func TestParseDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		want   string
		wantOK bool
	}{
		{name: "a friendlyName", body: deviceDescription("Living Room TV"), want: "Living Room TV", wantOK: true},
		{name: "white space folded", body: deviceDescription("\n  Living   Room\tTV \n"), want: "Living Room TV", wantOK: true},
		{name: "an escaped character", body: deviceDescription("Tom &amp; Jerry"), want: "Tom & Jerry", wantOK: true},
		{name: "no namespace", body: "<root><device><friendlyName>NAS</friendlyName></device></root>", want: "NAS", wantOK: true},
		{name: "64 characters", body: deviceDescription(strings.Repeat("a", 64)), want: strings.Repeat("a", 64), wantOK: true},
		{name: "65 characters", body: deviceDescription(strings.Repeat("a", 65))},
		{name: "white space only", body: deviceDescription("   ")},
		{name: "no friendlyName", body: "<root><device><manufacturer>Example</manufacturer></device></root>"},
		{name: "a control character", body: deviceDescription("TV\u0007")},
		{name: "a right-to-left override", body: deviceDescription("VT moor gniviL\u202e")},
		{name: "a zero-width space", body: deviceDescription("Living\u200bRoom TV")},
		{
			name:   "an ISO-8859-1 description",
			body:   "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><root><device><friendlyName>Caf\xe9 TV</friendlyName></device></root>",
			want:   "Café TV",
			wantOK: true,
		},
		{name: "not XML", body: "{\"name\": \"TV\"}"},
		{name: "an entity it does not define", body: deviceDescription("&lol;")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseDescription([]byte(tt.body))

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFetchFriendlyName(t *testing.T) {
	t.Parallel()

	ok := descriptionServer(t, deviceDescription("Living Room TV"))

	large := descriptionServer(t, deviceDescription("TV")+strings.Repeat(" ", maxDescriptionSize))

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, ok.URL+"/description.xml", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	tests := []struct {
		name   string
		url    string
		want   string
		wantOK bool
	}{
		{name: "a description", url: ok.URL + "/description.xml", want: "Living Room TV", wantOK: true},
		{name: "a 404", url: ok.URL + "/missing.xml"},
		{name: "a redirect, which is not followed", url: redirect.URL + "/description.xml"},
		{name: "a description over the size limit", url: large.URL + "/description.xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u, err := url.Parse(tt.url)
			require.NoError(t, err)

			got, ok := fetchFriendlyName(t.Context(), u)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFetchFriendlyNameStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	// The handler holds the request until the client gives up on it.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	u, err := url.Parse(srv.URL + "/description.xml")
	require.NoError(t, err)

	start := time.Now()

	_, ok := fetchFriendlyName(ctx, u)

	assert.False(t, ok)
	assert.Less(t, time.Since(start), descriptionTimeout)
}

func TestAskSSDPNamesTheHostsThatAnswer(t *testing.T) {
	t.Parallel()

	desc := descriptionServer(t, deviceDescription("Living Room TV"))
	group := newResponder(t, "127.0.0.1:0", nil, ssdpReply(desc.URL+"/description.xml"))

	addrs := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("127.0.0.2")}
	target := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), group.port())

	names, err := askSSDP(t.Context(), target, addrs, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, map[netip.Addr]string{netip.MustParseAddr("127.0.0.1"): "Living Room TV"}, names)
}

// Every device on the segment answers a search, so a host the sweep did not
// leave nameless is not fetched from.
func TestAskSSDPIgnoresAHostItWasNotAskedAbout(t *testing.T) {
	t.Parallel()

	desc := descriptionServer(t, deviceDescription("Living Room TV"))
	group := newResponder(t, "127.0.0.1:0", nil, ssdpReply(desc.URL+"/description.xml"))

	target := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), group.port())

	names, err := askSSDP(t.Context(), target, []netip.Addr{netip.MustParseAddr("127.0.0.2")}, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Empty(t, names)
}

// A description URL on another address than the one that answered is not
// fetched, so a device cannot point the fetch elsewhere.
func TestAskSSDPRefusesALocationOnAnotherAddress(t *testing.T) {
	t.Parallel()

	var fetched atomic.Bool

	desc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetched.Store(true)

		_, _ = w.Write([]byte(deviceDescription("Living Room TV")))
	}))
	t.Cleanup(desc.Close)

	// The description is on 127.0.0.1, and the answer comes from 127.0.0.4.
	group := newResponder(t, "127.0.0.4:0", nil, ssdpReply(desc.URL+"/description.xml"))
	target := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.4"), group.port())

	names, err := askSSDP(t.Context(), target, []netip.Addr{netip.MustParseAddr("127.0.0.4")}, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Empty(t, names)
	assert.False(t, fetched.Load())
}

// A host that cannot send the search fails it on every sweep, so only the
// first failure in a run reaches the caller.
func TestScannerReturnsOnlyTheFirstSSDPFailure(t *testing.T) {
	t.Parallel()

	s := New(slog.New(slog.DiscardHandler))

	// Port 0 is no destination, so the send fails.
	s.ssdpGroup = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)

	addrs := []netip.Addr{netip.MustParseAddr("127.0.0.1")}

	_, err := s.ssdpLookup(t.Context(), addrs)
	require.Error(t, err)

	_, err = s.ssdpLookup(t.Context(), addrs)
	require.NoError(t, err)
}

// A search that works ends a run of failures, so the next failure is the
// first of a new run and reaches the caller.
func TestScannerReturnsTheFirstSSDPFailureAfterASearchThatWorked(t *testing.T) {
	t.Parallel()

	s := New(slog.New(slog.DiscardHandler))
	addrs := []netip.Addr{netip.MustParseAddr("127.0.0.1")}

	// Port 0 is no destination, so the send fails.
	broken := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)
	working := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), newResponder(t, "127.0.0.1:0", nil, ssdpReply("")).port())

	s.ssdpGroup = broken
	_, err := s.ssdpLookup(t.Context(), addrs)
	require.Error(t, err)

	s.ssdpGroup = working
	_, err = s.ssdpLookup(t.Context(), addrs)
	require.NoError(t, err)

	s.ssdpGroup = broken
	_, err = s.ssdpLookup(t.Context(), addrs)
	require.Error(t, err)
}

// newSSDPResponder starts a responder on 127.0.0.1 that answers each search
// with one answer for each of locations, in the order given, and returns
// where to send the search.
func newSSDPResponder(t *testing.T, locations ...string) netip.AddrPort {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp4", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = pc.Close() })

	conn := pc.(*net.UDPConn)

	go func() {
		buf := make([]byte, 1500)

		for {
			n, from, err := conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}

			for _, loc := range locations {
				if raw, ok := ssdpReply(loc)(buf[:n]); ok {
					_, _ = conn.WriteToUDPAddrPort(raw, from)
				}
			}
		}
	}()

	return conn.LocalAddr().(*net.UDPAddr).AddrPort()
}

// A host with two root devices answers twice, in no set order. Its URLs are
// fetched in sorted order, so it gets the same name on every search.
func TestAskSSDPNamesAHostWithTwoRootDevicesTheSameEachTime(t *testing.T) {
	t.Parallel()

	gateway := descriptionServer(t, deviceDescription("Gateway"))
	media := descriptionServer(t, deviceDescription("Media Server"))

	gatewayURL := gateway.URL + "/description.xml"
	mediaURL := media.URL + "/description.xml"

	first, second := gatewayURL, mediaURL
	if mediaURL < gatewayURL {
		first, second = mediaURL, gatewayURL
	}

	want, err := fetchName(t, first)
	require.NoError(t, err)

	addrs := []netip.Addr{netip.MustParseAddr("127.0.0.1")}

	// The later URL is answered first, then the other way round.
	for _, order := range [][]string{{second, first}, {first, second}} {
		names, err := askSSDP(t.Context(), newSSDPResponder(t, order...), addrs, 200*time.Millisecond)
		require.NoError(t, err)

		assert.Equal(t, map[netip.Addr]string{netip.MustParseAddr("127.0.0.1"): want}, names)
	}
}

// When the first description does not fetch, the next one is tried.
func TestAskSSDPTriesTheNextDescriptionWhenOneFails(t *testing.T) {
	t.Parallel()

	desc := descriptionServer(t, deviceDescription("Router"))

	// /a.xml sorts before /description.xml, and the server answers it 404.
	missing := desc.URL + "/a.xml"
	found := desc.URL + "/description.xml"

	names, err := askSSDP(t.Context(), newSSDPResponder(t, found, missing), []netip.Addr{netip.MustParseAddr("127.0.0.1")}, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, map[netip.Addr]string{netip.MustParseAddr("127.0.0.1"): "Router"}, names)
}

// fetchName returns the friendlyName served at loc.
func fetchName(t *testing.T, loc string) (string, error) {
	t.Helper()

	u, err := url.Parse(loc)
	if err != nil {
		return "", err
	}

	name, ok := fetchFriendlyName(t.Context(), u)
	require.True(t, ok)

	return name, nil
}
