// Package plugin reads facts about devices from sources a sweep cannot reach.
//
// ARP is not routed, so a sweep learns hardware addresses for its own segment
// and no other. The router is the gateway for every VLAN, so its tables cover
// them all. Reading any such source raises the same two questions -- whether a
// sighting means the device is here now, and how much its name is worth --
// which is what [Fact] answers.
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
	// Plugin is one configured source: who it is, not what it can be asked for.
	//
	// Capabilities are separate interfaces because they do not yield the same
	// thing. The devices a router knows about, the port a device is plugged
	// into and the ports it is listening on are three different answers, and
	// one method returning any of them would return an empty interface.
	Plugin interface {
		// Name becomes the sources.name row these facts are filed under, so two
		// routers stay distinguishable without the schema knowing either exists.
		Name() string

		Kind() dbtype.SourceKind
	}

	// HostDiscoverer answers "which devices do you know about".
	HostDiscoverer interface {
		Plugin

		// Discover reads the source once. Facts and an error can both be
		// non-empty: one table answering while another times out is half a
		// router, and the half that arrived is true.
		Discover(ctx context.Context) ([]Fact, error)
	}

	// NetworkDiscoverer answers "which segments do you serve".
	NetworkDiscoverer interface {
		Plugin

		// Networks reads the source's segments once. Partial answers come back
		// the same way [HostDiscoverer.Discover] returns them.
		Networks(ctx context.Context) ([]Network, error)
	}
)

// Network is one segment a source serves.
//
// Nothing on the wire says which VLAN an address is on: the tag is stripped
// before a sweep ever sees a packet, so a segment's identity can only come
// from whatever is doing the routing.
type Network struct {
	// Prefix is masked, so a router reporting its own address with a length
	// meets the prefix a sweep recorded at the same value.
	Prefix netip.Prefix

	// Name is what a person called the segment, and is empty when nobody has.
	Name string

	// VLAN is the 802.1Q tag, zero on a segment that carries none. Untagged is
	// a real answer rather than a missing one.
	VLAN int
}

// Fact is what one source claims about one device at one moment.
type Fact struct {
	// Host is built through [hosts.BuildHost], so every plugin parses and
	// enriches the same way instead of each repeating the MAC parse and OUI
	// lookup. One address per fact; a device holding two is two facts.
	Host *hosts.Host

	// Present says the device is on the network now. A complete ARP entry is
	// evidence of that; a static lease for something unplugged is configuration,
	// and counting it would report an unplugged printer as online forever.
	Present bool

	// HostnameSource is the standing of the name Host carries. It travels with
	// the fact rather than with Kind, because one pass over a router yields a
	// lease an operator bound and a name a device asked for this hour, and they
	// do not weigh the same.
	HostnameSource dbtype.HostnameSource

	// Detail is what only this source knows, stored verbatim on the claim so
	// the device page can say why a source believes what it does.
	Detail map[string]string

	// SeenAt is when the source was read. The router measures age in durations
	// against a clock this process cannot see, so this is the only honest
	// timestamp available.
	SeenAt time.Time
}

// The distinction worth making about a failed read is whether the next attempt
// could go better.
var (
	// ErrUnreachable is a source that could not be contacted at all. Worth
	// retrying.
	ErrUnreachable = errors.New("plugin: source unreachable")

	// ErrAuth is a source that answered and refused the credentials. Retrying
	// will not fix it.
	ErrAuth = errors.New("plugin: source rejected credentials")
)
