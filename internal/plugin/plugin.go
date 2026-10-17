// Package plugin reads facts about devices from sources that a sweep cannot
// reach.
//
// ARP is not routed. So a sweep learns hardware addresses only for its own
// segment. The router is the gateway for every VLAN, so its tables cover all
// of them. Each such source has the same two questions, and [Fact] answers
// them. Does a sighting mean that the device is here now? How good is its
// name?
package plugin

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
)

type (
	// Plugin is one configured source. It tells only who it is.
	//
	// Capabilities are separate interfaces, because they give different
	// things. The devices that a router knows, the port that a device
	// connects to, and the ports that it listens on are three different
	// answers. One method for all of them would return an empty interface.
	Plugin interface {
		// Name becomes the sources.name row of these facts. So two routers
		// stay separate, and the schema does not need to know either.
		Name() string

		Kind() dbtype.SourceKind
	}

	// HostDiscoverer answers "which devices do you know about".
	HostDiscoverer interface {
		Plugin

		// Discover reads the source once. On error, the facts are nil. A
		// source that could not read every table returns none of them.
		Discover(ctx context.Context) ([]Fact, error)
	}

	// NetworkDiscoverer answers "which segments do you serve".
	NetworkDiscoverer interface {
		Plugin

		// Networks reads the segments of the source once. On error, the
		// networks are nil.
		Networks(ctx context.Context) ([]Network, error)
	}

	// TopologyReader answers "what is connected to what".
	TopologyReader interface {
		Plugin

		// Topology reads the switching tables of the source once. On error,
		// the Topology is empty.
		Topology(ctx context.Context) (Topology, error)
	}

	// TopologyScoped answers "are you read only for your switching tables".
	//
	// Jocasta reads a switch or access point for what is connected to it. The
	// router above it lists the devices and segments. So device discovery
	// leaves out a source that says this.
	TopologyScoped interface {
		Plugin

		IsTopologyOnly() bool
	}

	// TrafficReporter answers "who talks to whom".
	//
	// The router pushes to it. A router exports a flow when the conversation
	// ends or times out, on its own schedule. So Listen runs until ctx is
	// done. It gives each batch that it decodes to emit. It returns nil when
	// ctx is cancelled.
	TrafficReporter interface {
		Plugin

		Listen(ctx context.Context, emit func(context.Context, []Flow)) error
	}

	// QueryReader answers "what did each client look up".
	//
	// A resolver keeps its query log, and serves it on request. So Jocasta
	// reads the log on a schedule. Each read continues where the last read
	// stopped.
	QueryReader interface {
		Plugin

		// Queries reads what the resolver logged after cursor, oldest first.
		// It returns the cursor for the next read. An empty cursor starts one
		// hour back. Only the source knows what a cursor holds. The caller
		// stores it and gives it back.
		//
		// One read takes a limited part of a long log. A source that pages
		// oldest first returns a cursor in the middle of the log. So a
		// backlog empties over several reads. A source that pages newest
		// first keeps the newest part, and skips the rest. Queries and an
		// error can both be non-empty. The cursor then covers the returned
		// queries.
		Queries(ctx context.Context, cursor string) ([]Query, string, error)
	}
)

// Query is one lookup that a resolver logged.
type Query struct {
	// Client is the address that asked. A router that forwards the lookups
	// of its clients is the client of all of them.
	Client netip.Addr

	// Name is lowercase, without the trailing dot.
	Name string

	// Type is the record type of the lookup, such as A, AAAA or HTTPS.
	Type string

	// Blocked marks a lookup that the resolver refused because of a
	// blocklist. Failed marks a lookup that found nothing or failed upstream
	// (NXDOMAIN or SERVFAIL). Failed is false on a blocked lookup, because the
	// resolver chose that answer.
	Blocked bool
	Failed  bool

	// Answers is the addresses in the answer of the resolver, in order.
	Answers []netip.Addr

	// At is when the resolver logged the lookup.
	At time.Time
}

// Flow is one conversation that a source saw, in the recorded direction. The
// source tells which side sent. The reader must find which side is the
// device.
//
// Src is where the packets came from before NAT. Dst is where they arrived
// after NAT. On a router that does NAT, both are the local ends. Src is the
// device that an outgoing packet left. Dst is the device that a reply reached
// after the router translated its public address.
type Flow struct {
	Src, Dst         netip.Addr
	SrcPort, DstPort uint16

	// Protocol is the IANA protocol number: 6 TCP, 17 UDP, 1 ICMP.
	Protocol uint8

	// TCPFlags is the TCP flags that the source reported. It is zero when the
	// source exports none. Sources give it different meanings. Some OR the
	// flags of every packet. A MikroTik reports only the flags of the first
	// packet. So the code uses only what both meanings share. A flow that
	// opens a connection shows SYN. The answer shows SYN and ACK, or RST when
	// refused.
	TCPFlags uint8

	// ICMPType is the ICMP message type. It tells an echo request (8, or 128
	// for ICMPv6) from its reply. It is zero when the flow is not ICMP, or the
	// source does not export it.
	ICMPType uint8

	// Bytes and Packets are what the source estimates went through, already
	// scaled by its sampling rate. A source that samples one packet in a
	// hundred reports what it saw times a hundred. This is an estimate, but it
	// is the only number worth a total.
	Bytes, Packets uint64

	// End is when the source last saw the conversation. It decides the hour
	// of the count. If the source exports a long transfer in pieces, each
	// piece goes in the hour when it ended.
	End time.Time

	// Exporter is the address of the router that reported the flow. With it,
	// traffic to the router itself stays separate.
	Exporter netip.Addr

	// NATSrc is the address to which the router changed Src on the way out.
	// On a router that does NAT, this is its public address. It is zero when
	// the source was not translated, or the exporter does not tell.
	NATSrc netip.Addr
}

// Network is one segment that a source serves.
//
// No packet tells the VLAN of an address. The tag is removed before a sweep
// sees a packet. So the identity of a segment can come only from the device
// that routes.
type Network struct {
	// Prefix is masked. So a router that reports its own address with a
	// length gives the same value as the prefix that a sweep recorded.
	Prefix netip.Prefix

	// Name is the name that a person gave the segment. It is empty when
	// nobody gave one.
	Name string

	// VLAN is the 802.1Q tag. It is zero on a segment with no tag. Untagged is
	// a real answer.
	VLAN int
}

// Topology is what one switching device knows about what is connected to it:
// its ports, the hardware addresses that it learned on each, and the
// neighbours that announced themselves. The tree of the whole network comes
// from one Topology for each router, switch and access point.
type Topology struct {
	// Identity is the name that the device gives itself.
	Identity string

	// Gateway marks the device that routes the network. The tree hangs from
	// it, below the internet.
	Gateway bool

	// Own is the hardware addresses of the device. The tables of other
	// devices recognise it by these addresses.
	Own []string

	Ports      []TopologyPort
	Seen       []Sighting
	Neighbours []Neighbour

	// ReadAt is when the source was read.
	ReadAt time.Time
}

// Empty reports whether a read learned nothing about what is connected: no
// address on a port, and no neighbour.
func (t Topology) Empty() bool { return len(t.Seen) == 0 && len(t.Neighbours) == 0 }

// PortKind is the kind of interface of a port.
type PortKind string

// The tree draws a wired port and a radio differently. Anything else (a
// bridge, a VLAN interface, a tunnel) is only a place where a device can learn
// an address.
const (
	PortWired   PortKind = "wired"
	PortWiFi    PortKind = "wifi"
	PortVirtual PortKind = "virtual"
)

// TopologyPort is one interface, with the VLANs that it carries.
type TopologyPort struct {
	Name string
	Kind PortKind

	// PVID is the VLAN of the untagged traffic on the port. It is zero when
	// the device does not tell. Tagged and Untagged are the VLANs that the
	// port carries in each way, in order.
	PVID     int
	Tagged   []int
	Untagged []int

	Running bool

	// Rate is the speed at which the link of the port came up. Capable is the
	// fastest speed that both ends offer. Both are in bits per second, and
	// zero when unknown. FullDuplex has no meaning without a Rate.
	Rate       int64
	Capable    int64
	FullDuplex bool
}

// Sighting is one hardware address that a device learned on one port.
type Sighting struct {
	Port string
	MAC  string

	// VLAN is the VLAN in which the device learned the address. It is zero
	// when the device does not tell.
	VLAN int

	// WiFi marks a client on a radio of the device. SSID and Band describe
	// the radio.
	WiFi bool
	SSID string
	Band string

	// TxRate is the rate at which the radio sends to the client. RxRate is
	// the rate at which it receives from the client. Both are in bits per
	// second. Signal is the signal of the client at the radio, in dBm. Each is
	// zero when unknown.
	TxRate int64
	RxRate int64
	Signal int
}

// Neighbour is a device that announced itself on one port.
type Neighbour struct {
	Port string
	MAC  string
	Addr netip.Addr

	Identity string
	Platform string
	Board    string

	// TheirPort is the name that the neighbour gives the port from which it
	// announced itself.
	TheirPort string
}

// Fact is what one source says about one device at one time.
type Fact struct {
	// Host is built through [hosts.BuildHost]. So every plugin parses and
	// enriches in the same way. Each fact has one address. A device with two
	// addresses is two facts.
	Host *hosts.Host

	// Present tells that the device is on the network now. It answered a
	// probe, or a source that reads the network heard from it recently. A
	// resolved ARP entry is only a record of the device, if the router did
	// not hear from it since. A static lease for a disconnected device is
	// also only a record. If Jocasta counted either as a sighting, it would
	// report the device as online long after it left.
	Present bool

	// HostnameSource is the standing of the name of Host. It goes with each
	// fact. One pass over a router gives a lease that an operator bound, and
	// a name that a device asked for this hour. They do not have the same
	// value.
	HostnameSource dbtype.HostnameSource

	// Detail is what only this source knows. It is stored exactly on the
	// claim. So the device page can tell why a source believes what it
	// says.
	Detail map[string]string

	// Services are what the device advertised over DNS-SD. Only a sweep
	// browses for them. A router leaves this empty.
	Services []hosts.Service

	// SeenAt is when the source was read. The router measures age in
	// durations against a clock that this process cannot see. So this is the
	// only true timestamp.
	SeenAt time.Time
}

// For a failed read, the important question is whether the next attempt can go
// better.
var (
	// ErrUnreachable is a source that Jocasta cannot contact at all. A retry
	// can help.
	ErrUnreachable = errors.New("plugin: source unreachable")

	// ErrAuth is a source that answered and refused the credentials. A retry
	// does not help.
	ErrAuth = errors.New("plugin: source rejected credentials")
)
