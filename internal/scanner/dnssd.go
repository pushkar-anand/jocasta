package scanner

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/hosts"
	"golang.org/x/net/dns/dnsmessage"
)

// standardDNSSDGroup is the multicast group and port of mDNS queries (RFC
// 6762, section 3).
var standardDNSSDGroup = netip.MustParseAddrPort("224.0.0.251:5353")

// serviceTypesName is the name that a query asks about to learn every service
// type on the segment (RFC 6763, section 9).
const serviceTypesName = "_services._dns-sd._udp.local."

// dnssdWait is how long to read answers to each browse query. A responder can
// hold an answer that other hosts could also give for up to 500 ms (RFC 6762,
// section 6). askWait covers its arrival.
const dnssdWait = 500*time.Millisecond + askWait

// Limits on what one browse keeps. Every host on the segment answers, and each
// writes its own answer. So both have a limit.
const (
	// maxServiceTypes limits the service types that the second query asks
	// about. A home segment advertises a few dozen at most.
	maxServiceTypes = 32

	// maxServicesPerHost limits the services kept from one address.
	maxServicesPerHost = 16
)

// castService is the service type of a Google Cast device. Its TXT record has
// the name that its owner gave it, and its model. No other service type has
// these.
const castService = "_googlecast._tcp"

// castGroupModel is the model of a Google Cast speaker group. A speaker
// advertises each group that it leads. So the name of a group is not the name
// of the speaker.
const castGroupModel = "Google Cast Group"

// browseDNSSD asks group for every service type on the segment, then for the
// instances of each type. It returns the services of each IPv4 address in
// addrs, sorted by type and instance. An address that advertised nothing is
// absent. It reads answers to each query for wait.
//
// A service goes under the address of its SRV target in the same answer, if
// the answer gives one. So a host that answers for a different host, as a
// sleep proxy does, does not take the services of that host.
//
// It returns an error only when a query cannot be sent. If ctx ends first, it
// returns the services that arrived before, with the error of ctx.
func browseDNSSD(
	ctx context.Context,
	group netip.AddrPort,
	addrs []netip.Addr,
	wait time.Duration,
) (map[netip.Addr][]hosts.Service, error) {
	services := make(map[netip.Addr][]hosts.Service)

	asked := make(map[netip.Addr]bool, len(addrs))

	for _, a := range addrs {
		if a.Is4() {
			asked[a] = true
		}
	}

	if len(asked) == 0 {
		return services, nil
	}

	pc, err := dnssdConn(group)
	if err != nil {
		return services, fmt.Errorf("open DNS-SD socket: %w", err)
	}
	defer func() { _ = pc.Close() }()

	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()) })
	defer stop()

	types := make(map[string]bool)

	err = ask(ctx, pc, group, wait, []string{serviceTypesName}, func(from netip.Addr, b []byte) {
		if !asked[from] {
			return
		}

		for _, t := range parseServiceTypes(b) {
			types[t] = true
		}
	})
	if err != nil || len(types) == 0 {
		return services, cmp.Or(err, ctx.Err())
	}

	// Sort them. So a segment with more types than the limit asks about the
	// same types on every sweep.
	names := slices.Sorted(maps.Keys(types))
	names = names[:min(len(names), maxServiceTypes)]

	err = ask(ctx, pc, group, wait, names, func(from netip.Addr, b []byte) {
		for _, o := range parseInstances(b, names, from) {
			if !asked[o.addr] || len(services[o.addr]) >= maxServicesPerHost {
				continue
			}

			// A responder repeats its answer when it sees the question of a
			// different querier. So the same instance can arrive twice.
			if !slices.ContainsFunc(services[o.addr], func(k hosts.Service) bool { return k.Type == o.Type && k.Instance == o.Instance }) {
				services[o.addr] = append(services[o.addr], o.Service)
			}
		}
	})

	for _, s := range services {
		slices.SortFunc(s, func(a, b hosts.Service) int {
			return cmp.Or(strings.Compare(a.Type, b.Type), strings.Compare(a.Instance, b.Instance))
		})
	}

	return services, cmp.Or(err, ctx.Err())
}

// dnssdConn opens the socket on which a browse sends and reads. For a multicast
// group, it binds the port of the group and joins the group. So answers to the
// group arrive. A responder answers a query from the mDNS port to the group
// (RFC 6762, section 6). A stateful firewall lets that answer in. It would
// drop a unicast answer to a query that it saw go to a group. Any mDNS
// responder on this machine, such as Avahi, shares the port.
//
// Any other group is one responder, as in a test. It answers to the port of
// the query.
func dnssdConn(group netip.AddrPort) (*net.UDPConn, error) {
	if !group.Addr().IsMulticast() {
		return net.ListenUDP("udp4", nil)
	}

	return net.ListenMulticastUDP("udp4", nil, net.UDPAddrFromAddrPort(group))
}

// ask sends a PTR query for names to group. It gives each message that arrives
// within wait to read, with the IPv4 address that it came from. It returns an
// error only when the query cannot be built or sent.
func ask(
	ctx context.Context,
	pc *net.UDPConn,
	group netip.AddrPort,
	wait time.Duration,
	names []string,
	read func(from netip.Addr, b []byte),
) error {
	if ctx.Err() != nil {
		return nil
	}

	q, err := ptrQuery(names...)
	if err != nil {
		return fmt.Errorf("build DNS-SD query: %w", err)
	}

	if _, err := pc.WriteToUDPAddrPort(q, group); err != nil {
		return fmt.Errorf("send DNS-SD query: %w", err)
	}

	_ = pc.SetReadDeadline(time.Now().Add(wait))

	// ctx can end after the last change of the deadline. The deadline set just
	// now would undo that change.
	if ctx.Err() != nil {
		_ = pc.SetReadDeadline(time.Now())
	}

	// An mDNS message can fill a jumbo frame (RFC 6762, section 17).
	buf := make([]byte, 9000)

	for {
		// The read deadline fails this read when the wait is over, or when
		// ctx ends.
		n, from, err := pc.ReadFromUDPAddrPort(buf)
		if err != nil {
			return nil
		}

		read(from.Addr().Unmap(), buf[:n])
	}
}

// parseServiceTypes returns the service types that the response in b lists in
// answer to serviceTypesName, such as "_googlecast._tcp.local.". It returns
// none when b is not a response. This includes the query of this machine that
// loops back.
func parseServiceTypes(b []byte) []string {
	var types []string

	for _, r := range responseRecords(b) {
		ptr, ok := r.Body.(*dnsmessage.PTRResource)
		if !ok || !strings.EqualFold(r.Header.Name.String(), serviceTypesName) {
			continue
		}

		// A type is two labels under local., such as _http._tcp.local.
		t := strings.ToLower(ptr.PTR.String())
		if strings.Count(t, ".") == 3 && strings.HasSuffix(t, ".local.") && strings.HasPrefix(t, "_") {
			types = append(types, t)
		}
	}

	return types
}

// owned is a service, with the address that runs it.
type owned struct {
	addr netip.Addr
	hosts.Service
}

// parseInstances returns the service instances of the types in types, from the
// response in b. The response came from from. Each service goes under the
// address that runs it. This is the address of its SRV target in b, if b gives
// one. If not, it is from. If the target of an instance resolves only to
// addresses other than from, the instance goes under the lowest of them.
func parseInstances(b []byte, types []string, from netip.Addr) []owned {
	type instance struct {
		name   string // as the host spells it, which the label keeps
		typ    string
		port   uint16
		target string
		txt    []string
	}

	// Names match without case (RFC 6762, section 16). So both maps have
	// lowercase keys.
	instances := make(map[string]*instance)
	targets := make(map[string][]netip.Addr)

	records := responseRecords(b)

	// PTRs first. So an SRV or TXT stays only for an instance of an asked
	// type, in any order of the records.
	for _, r := range records {
		ptr, ok := r.Body.(*dnsmessage.PTRResource)
		if !ok {
			continue
		}

		typ := strings.ToLower(r.Header.Name.String())
		full := ptr.PTR.String()

		if slices.Contains(types, typ) {
			instances[strings.ToLower(full)] = &instance{name: full, typ: typ}
		}
	}

	for _, r := range records {
		name := strings.ToLower(r.Header.Name.String())

		switch body := r.Body.(type) {
		case *dnsmessage.SRVResource:
			if in, ok := instances[name]; ok {
				in.port = body.Port
				in.target = strings.ToLower(body.Target.String())
			}
		case *dnsmessage.TXTResource:
			if in, ok := instances[name]; ok {
				in.txt = body.TXT
			}
		case *dnsmessage.AResource:
			targets[name] = append(targets[name], netip.AddrFrom4(body.A))
		}
	}

	found := make([]owned, 0, len(instances))

	for _, in := range instances {
		owner := from

		if addrs := targets[in.target]; len(addrs) > 0 && !slices.Contains(addrs, from) {
			owner = slices.MinFunc(addrs, netip.Addr.Compare)
		}

		s := hosts.Service{
			Type: strings.TrimSuffix(in.typ, ".local."),
			Port: in.port,
		}

		// The instance is every label before the labels of the type. It is
		// the text of the host, so it can contain a dot.
		if cut := len(in.name) - len(in.typ) - 1; cut > 0 && in.name[cut] == '.' {
			s.Instance, _ = cleanLabel(in.name[:cut])
		}

		if s.Type == castService {
			s.Label, s.Model = castTXT(in.txt)
		}

		found = append(found, owned{addr: owner, Service: s})
	}

	return found
}

// castTXT returns the fn and md values of a Google Cast TXT record. These are
// the name that the owner of the device gave it, and its model. Each is empty
// when the record does not have it, or cleanLabel refuses it.
func castTXT(txt []string) (label, model string) {
	for _, kv := range txt {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}

		switch strings.ToLower(k) {
		case "fn":
			label, _ = cleanLabel(v)
		case "md":
			model, _ = cleanLabel(v)
		}
	}

	return label, model
}

// responseRecords returns every answer and additional record in the response
// in b. It returns none when b is not a response, or does not parse. It drops
// the records after a record that does not parse.
func responseRecords(b []byte) []dnsmessage.Resource {
	var p dnsmessage.Parser

	h, err := p.Start(b)
	if err != nil || !h.Response {
		return nil
	}

	if err := p.SkipAllQuestions(); err != nil {
		return nil
	}

	answers, err := p.AllAnswers()
	if err != nil {
		return answers
	}

	if err := p.SkipAllAuthorities(); err != nil {
		return answers
	}

	additionals, _ := p.AllAdditionals()

	return append(answers, additionals...)
}

// Patterns that dnssdName removes from an instance name, or that make it refuse
// the name.
var (
	// machineID matches a series of hex digits long enough to be an ID, or a
	// UUID, as in "Android_0123456789abcdef0123456789abcdef".
	machineID = regexp.MustCompile(`(?i)[0-9a-f]{16}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

	// macPrefix matches the hardware address that AirPlay puts before the
	// name of a speaker, as in "00005E005301@Kitchen". macSuffix matches the
	// hardware address that Avahi puts after the name of a workstation, as in
	// "host-a [00:00:5e:00:53:01]".
	macPrefix = regexp.MustCompile(`^[0-9A-Fa-f]{12}@`)
	macSuffix = regexp.MustCompile(`\s*\[[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}\]$`)
)

// dnssdName returns the name for a host that advertises services. It returns
// false when no service gives a name. The name that the owner of a Google Cast
// device gave it wins. Else, it is the instance name that most services share,
// after it removes hardware addresses. It leaves out names with a machine ID.
// If two names have the same count, the first in sort order wins. So the name
// is the same on every sweep.
func dnssdName(services []hosts.Service) (string, bool) {
	for _, s := range services {
		if s.Label != "" && s.Model != castGroupModel {
			return s.Label, true
		}
	}

	counts := make(map[string]int)

	for _, s := range services {
		name := macSuffix.ReplaceAllString(macPrefix.ReplaceAllString(s.Instance, ""), "")
		if name == "" || machineID.MatchString(name) {
			continue
		}

		counts[name]++
	}

	if len(counts) == 0 {
		return "", false
	}

	return slices.MaxFunc(slices.Sorted(maps.Keys(counts)), func(a, b string) int {
		// A tie goes to the earlier name. MaxFunc keeps it as the first
		// maximal element.
		return cmp.Compare(counts[a], counts[b])
	}), true
}
