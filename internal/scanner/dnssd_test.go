package scanner

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

// advert is one service instance a test responder advertises.
type advert struct {
	typ      string // such as "_googlecast._tcp.local."
	instance string // such as "Living Room TV"
	port     uint16
	target   string     // the SRV target, such as "tv.local."
	txt      []string   // left out when empty
	a        netip.Addr // the target's A record, left out when invalid
}

// dnssdResponse builds a response answering serviceTypesName with the type of
// each of ads when types is set, and otherwise giving each of ads in full: a
// PTR from its type, its SRV and TXT, and its target's A record.
func dnssdResponse(t *testing.T, types bool, ads []advert) []byte {
	t.Helper()

	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	require.NoError(t, b.StartAnswers())

	h := func(name string) dnsmessage.ResourceHeader {
		return dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Class: dnsmessage.ClassINET, TTL: 120}
	}

	for _, ad := range ads {
		if types {
			require.NoError(t, b.PTRResource(h(serviceTypesName), dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(ad.typ)}))

			continue
		}

		full := ad.instance + "." + ad.typ

		require.NoError(t, b.PTRResource(h(ad.typ), dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(full)}))
		require.NoError(t, b.SRVResource(h(full), dnsmessage.SRVResource{Port: ad.port, Target: dnsmessage.MustNewName(ad.target)}))

		if len(ad.txt) > 0 {
			require.NoError(t, b.TXTResource(h(full), dnsmessage.TXTResource{TXT: ad.txt}))
		}

		if ad.a.IsValid() {
			require.NoError(t, b.AResource(h(ad.target), dnsmessage.AResource{A: ad.a.As4()}))
		}
	}

	raw, err := b.Finish()
	require.NoError(t, err)

	return raw
}

// dnssdReply answers a query for serviceTypesName with the type of each of
// ads, and a query for service types with each of ads whose type it asks
// about. It stays silent when it has nothing to give.
func dnssdReply(t *testing.T, ads ...advert) func([]byte) ([]byte, bool) {
	t.Helper()

	return func(query []byte) ([]byte, bool) {
		var p dnsmessage.Parser

		if _, err := p.Start(query); err != nil {
			return nil, false
		}

		questions, err := p.AllQuestions()
		if err != nil || len(questions) == 0 {
			return nil, false
		}

		if questions[0].Name.String() == serviceTypesName {
			return dnssdResponse(t, true, ads), len(ads) > 0
		}

		var asked []advert

		for _, ad := range ads {
			if slices.ContainsFunc(questions, func(q dnsmessage.Question) bool { return q.Name.String() == ad.typ }) {
				asked = append(asked, ad)
			}
		}

		return dnssdResponse(t, false, asked), len(asked) > 0
	}
}

// livingRoomTV is what an Android TV advertises.
var livingRoomTV = []advert{
	{
		typ: "_googlecast._tcp.local.", instance: "Android_0123456789abcdef0123456789abcdef", port: 8009, target: "tv.local.",
		txt: []string{"id=0123456789abcdef", "md=Chromecast HD", "fn=Living Room TV"},
	},
	{typ: "_androidtvremote2._tcp.local.", instance: "Living Room TV", port: 6466, target: "tv.local."},
}

func TestBrowseDNSSD(t *testing.T) {
	t.Parallel()

	r := newResponder(t, "127.0.0.1:0", nil, dnssdReply(t, livingRoomTV...))
	group := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), r.port())
	addr := netip.MustParseAddr("127.0.0.1")

	services, err := browseDNSSD(t.Context(), group, []netip.Addr{addr}, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, []Service{
		{Type: "_androidtvremote2._tcp", Instance: "Living Room TV", Port: 6466},
		{
			Type: "_googlecast._tcp", Instance: "Android_0123456789abcdef0123456789abcdef", Port: 8009,
			Label: "Living Room TV", Model: "Chromecast HD",
		},
	}, services[addr])
	assert.Equal(t, int32(2), r.queries.Load())
}

// Every host on the segment answers, so a browse keeps only the addresses it
// was asked about, and asks for no instances when none of them gave a type.
func TestBrowseDNSSDIgnoresAddressesNotAsked(t *testing.T) {
	t.Parallel()

	r := newResponder(t, "127.0.0.4:0", nil, dnssdReply(t, livingRoomTV...))
	group := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.4"), r.port())

	services, err := browseDNSSD(t.Context(), group, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Empty(t, services)
	assert.Equal(t, int32(1), r.queries.Load())
}

// A host answering for another, as a sleep proxy does, gives the other host's
// address for the service, which is where the service is filed.
func TestBrowseDNSSDFilesAServiceUnderItsTarget(t *testing.T) {
	t.Parallel()

	proxied := advert{
		typ: "_airplay._tcp.local.", instance: "Office", port: 7000,
		target: "office.local.", a: netip.MustParseAddr("127.0.0.2"),
	}

	r := newResponder(t, "127.0.0.1:0", nil, dnssdReply(t, proxied))
	group := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), r.port())

	services, err := browseDNSSD(t.Context(), group,
		[]netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("127.0.0.2")}, 200*time.Millisecond)
	require.NoError(t, err)

	assert.NotContains(t, services, netip.MustParseAddr("127.0.0.1"))
	assert.Equal(t, []Service{{Type: "_airplay._tcp", Instance: "Office", Port: 7000}}, services[netip.MustParseAddr("127.0.0.2")])
}

func TestParseServiceTypes(t *testing.T) {
	t.Parallel()

	query, err := ptrQuery(serviceTypesName)
	require.NoError(t, err)

	ads := []advert{
		{typ: "_googlecast._tcp.local."},
		{typ: "_HAP._TCP.local."},
		{typ: "_sub._googlecast._tcp.local."},
		{typ: "_ipp._tcp.example.com."},
	}

	assert.Equal(t, []string{"_googlecast._tcp.local.", "_hap._tcp.local."}, parseServiceTypes(dnssdResponse(t, true, ads)))
	assert.Empty(t, parseServiceTypes(query), "this machine's own query")
	assert.Empty(t, parseServiceTypes([]byte{0x00, 0x01}))
}

func TestParseInstances(t *testing.T) {
	t.Parallel()

	from := netip.MustParseAddr("192.0.2.10")
	types := []string{"_googlecast._tcp.local.", "_airplay._tcp.local."}

	tests := []struct {
		name string
		ad   advert
		want []owned
	}{
		{
			name: "the instance keeps its case",
			ad:   advert{typ: "_airplay._tcp.local.", instance: "Living Room TV", port: 7000, target: "tv.local."},
			want: []owned{{from, Service{Type: "_airplay._tcp", Instance: "Living Room TV", Port: 7000}}},
		},
		{
			name: "a dot in the instance",
			ad:   advert{typ: "_airplay._tcp.local.", instance: "Mr. Speaker", port: 7000, target: "speaker.local."},
			want: []owned{{from, Service{Type: "_airplay._tcp", Instance: "Mr. Speaker", Port: 7000}}},
		},
		{
			name: "the Cast name and model",
			ad: advert{
				typ: "_googlecast._tcp.local.", instance: "Nest-Mini-0123", port: 8009, target: "nest.local.",
				txt: []string{"md=Google Nest Mini", "FN=Kitchen speaker", "rs"},
			},
			want: []owned{{from, Service{
				Type: "_googlecast._tcp", Instance: "Nest-Mini-0123", Port: 8009,
				Label: "Kitchen speaker", Model: "Google Nest Mini",
			}}},
		},
		{
			name: "a target on the address that answered",
			ad:   advert{typ: "_airplay._tcp.local.", instance: "TV", port: 7000, target: "tv.local.", a: from},
			want: []owned{{from, Service{Type: "_airplay._tcp", Instance: "TV", Port: 7000}}},
		},
		{
			name: "a target on another address",
			ad: advert{
				typ: "_airplay._tcp.local.", instance: "TV", port: 7000, target: "tv.local.",
				a: netip.MustParseAddr("192.0.2.11"),
			},
			want: []owned{{netip.MustParseAddr("192.0.2.11"), Service{Type: "_airplay._tcp", Instance: "TV", Port: 7000}}},
		},
		{
			name: "a type not asked about",
			ad:   advert{typ: "_ipp._tcp.local.", instance: "Printer", port: 631, target: "printer.local."},
			want: []owned{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, parseInstances(dnssdResponse(t, false, []advert{tt.ad}), types, from))
		})
	}
}

func TestDNSSDName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		services []Service
		want     string
		wantOK   bool
	}{
		{
			name: "the Cast name wins",
			services: []Service{
				{Type: "_airplay._tcp", Instance: "Bedroom"},
				{Type: "_googlecast._tcp", Instance: "Nest-Mini-0123", Label: "Kitchen speaker", Model: "Google Nest Mini"},
			},
			want: "Kitchen speaker", wantOK: true,
		},
		{
			name: "a Cast group the speaker leads does not name it",
			services: []Service{
				{Type: "_googlecast._tcp", Instance: "Google-Cast-Group-0123456789abcdef", Label: "Downstairs", Model: castGroupModel},
				{Type: "_spotify-connect._tcp", Instance: "Kitchen"},
			},
			want: "Kitchen", wantOK: true,
		},
		{
			name: "the name most services share",
			services: []Service{
				{Type: "_airplay._tcp", Instance: "Living Room TV"},
				{Type: "_androidtvremote2._tcp", Instance: "Living Room TV"},
				{Type: "_http._tcp", Instance: "Web interface"},
			},
			want: "Living Room TV", wantOK: true,
		},
		{
			name: "a tie goes to the first in sort order",
			services: []Service{
				{Type: "_http._tcp", Instance: "Office printer"},
				{Type: "_ipp._tcp", Instance: "Laser"},
			},
			want: "Laser", wantOK: true,
		},
		{
			name:     "the AirPlay hardware address is stripped",
			services: []Service{{Type: "_raop._tcp", Instance: "00005E005301@Kitchen"}},
			want:     "Kitchen", wantOK: true,
		},
		{
			name:     "the Avahi hardware address is stripped",
			services: []Service{{Type: "_workstation._tcp", Instance: "host-a [00:00:5e:00:53:01]"}},
			want:     "host-a", wantOK: true,
		},
		{
			name: "machine IDs are left out",
			services: []Service{
				{Type: "_googlecast._tcp", Instance: "Android_0123456789abcdef0123456789abcdef"},
				{Type: "_matter._tcp", Instance: "00000000-0000-0000-0000-000000000001"},
			},
		},
		{name: "nothing advertised"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := dnssdName(tt.services)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
