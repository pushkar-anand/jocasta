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

	"golang.org/x/net/dns/dnsmessage"
)

// standardDNSSDGroup is the multicast group and port mDNS queries go to (RFC
// 6762, section 3).
var standardDNSSDGroup = netip.MustParseAddrPort("224.0.0.251:5353")

// serviceTypesName is the name a query asks about to learn every service type
// advertised on the segment (RFC 6763, section 9).
const serviceTypesName = "_services._dns-sd._udp.local."

// dnssdWait is how long to read answers to each browse query. A responder
// may hold back an answer that other hosts could also give for up to 500 ms
// (RFC 6762, section 6), and askWait covers its arrival.
const dnssdWait = 500*time.Millisecond + askWait

// Limits on what one browse keeps. Every host on the segment answers, and each
// writes its own answer, so both are bounded.
const (
	// maxServiceTypes caps the service types the second query asks about. A
	// home segment advertises a few dozen at most.
	maxServiceTypes = 32

	// maxServicesPerHost caps the services kept from one address.
	maxServicesPerHost = 16
)

// castService is the service type a Google Cast device advertises. Its TXT
// record carries the name its owner gave it and its model, which no other
// service type does.
const castService = "_googlecast._tcp"

// castGroupModel is the model a Google Cast speaker group gives. A speaker
// advertises each group it leads, so a group's name is not the speaker's.
const castGroupModel = "Google Cast Group"

// Service is one service a host advertises over DNS-SD.
type Service struct {
	// Type is the service type without the domain, such as
	// "_googlecast._tcp".
	Type string `json:"type"`

	// Instance is the name the host gives the service, such as "Living Room
	// TV", and is empty when that name is one cleanLabel refuses.
	Instance string `json:"instance,omitempty"`

	// Port is the port the service listens on, and zero when no SRV record
	// came with it.
	Port uint16 `json:"port,omitempty"`

	// Label and Model are the name a Google Cast device shows its owner and
	// its model, such as "Google Nest Mini", from its TXT record. Both are
	// empty for any other service type.
	Label string `json:"label,omitempty"`
	Model string `json:"model,omitempty"`
}

// browseDNSSD asks group for every service type advertised on the segment, then
// for the instances of each type, and returns the services each IPv4 address
// in addrs advertises, sorted by type and instance. An address that advertised
// nothing is absent. It reads answers to each query for wait.
//
// A service is filed under the address its SRV target resolves to in the same
// answer, where the answer gives one, so a host answering for another, as a
// sleep proxy does, does not take that host's services.
//
// It returns an error only when a query cannot be sent. When ctx ends first,
// it returns the services that arrived before then, with ctx's error.
func browseDNSSD(
	ctx context.Context,
	group netip.AddrPort,
	addrs []netip.Addr,
	wait time.Duration,
) (map[netip.Addr][]Service, error) {
	services := make(map[netip.Addr][]Service)

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

	// Sorted, so a segment with more types than the cap asks about the same
	// ones on every sweep.
	names := slices.Sorted(maps.Keys(types))
	names = names[:min(len(names), maxServiceTypes)]

	err = ask(ctx, pc, group, wait, names, func(from netip.Addr, b []byte) {
		for _, o := range parseInstances(b, names, from) {
			if !asked[o.addr] || len(services[o.addr]) >= maxServicesPerHost {
				continue
			}

			// A responder repeats its answer when it sees another querier's
			// question, so the same instance can arrive twice.
			if !slices.ContainsFunc(services[o.addr], func(k Service) bool { return k.Type == o.Type && k.Instance == o.Instance }) {
				services[o.addr] = append(services[o.addr], o.Service)
			}
		}
	})

	for _, s := range services {
		slices.SortFunc(s, func(a, b Service) int {
			return cmp.Or(strings.Compare(a.Type, b.Type), strings.Compare(a.Instance, b.Instance))
		})
	}

	return services, cmp.Or(err, ctx.Err())
}

// dnssdConn opens the socket a browse sends from and reads on. For a multicast
// group it binds the group's port and joins the group, so answers sent to the
// group arrive. A responder answers a query from the mDNS port to the group
// (RFC 6762, section 6), and a stateful firewall lets that in where it drops
// a unicast answer to a query it saw go to a group. The port is shared with
// any mDNS responder on this machine, such as Avahi.
//
// Any other group is one responder, as in a test, which answers to the
// query's own port.
func dnssdConn(group netip.AddrPort) (*net.UDPConn, error) {
	if !group.Addr().IsMulticast() {
		return net.ListenUDP("udp4", nil)
	}

	return net.ListenMulticastUDP("udp4", nil, net.UDPAddrFromAddrPort(group))
}

// ask sends a PTR query for names to group, and hands each message that
// arrives within wait to read, with the IPv4 address it came from. It returns
// an error only when the query cannot be built or sent.
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

	// ctx may have ended after the deadline was last cut short, which the
	// deadline just set would undo.
	if ctx.Err() != nil {
		_ = pc.SetReadDeadline(time.Now())
	}

	// An mDNS message can fill a jumbo frame (RFC 6762, section 17).
	buf := make([]byte, 9000)

	for {
		// The read deadline is what fails this read once the wait is over or
		// ctx ends.
		n, from, err := pc.ReadFromUDPAddrPort(buf)
		if err != nil {
			return nil
		}

		read(from.Addr().Unmap(), buf[:n])
	}
}

// parseServiceTypes returns the service types the response in b lists in
// answer to serviceTypesName, such as "_googlecast._tcp.local.". It returns
// none when b is not a response, including this machine's own query looping
// back.
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

// owned is a service and the address that runs it.
type owned struct {
	addr netip.Addr
	Service
}

// parseInstances returns the services the response in b, which came from
// from, gives instances of for the service types in types. Each is filed under
// the address that runs it: the address its SRV target resolves to in b if b
// gives one, and from otherwise. An instance whose target resolves only to
// addresses other than from is filed under the lowest of them.
func parseInstances(b []byte, types []string, from netip.Addr) []owned {
	type instance struct {
		name   string // as the host spells it, which the label keeps
		typ    string
		port   uint16
		target string
		txt    []string
	}

	// Names are matched without regard to case (RFC 6762, section 16), so
	// both maps are keyed in lower case.
	instances := make(map[string]*instance)
	hosts := make(map[string][]netip.Addr)

	records := responseRecords(b)

	// PTRs first, so an SRV or TXT is kept only for an instance of an asked
	// type, whatever order the records come in.
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
			hosts[name] = append(hosts[name], netip.AddrFrom4(body.A))
		}
	}

	found := make([]owned, 0, len(instances))

	for _, in := range instances {
		owner := from

		if addrs := hosts[in.target]; len(addrs) > 0 && !slices.Contains(addrs, from) {
			owner = slices.MinFunc(addrs, netip.Addr.Compare)
		}

		s := Service{
			Type: strings.TrimSuffix(in.typ, ".local."),
			Port: in.port,
		}

		// The instance is every label before the type's. It is the host's own
		// text, so it can hold a dot.
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

// castTXT returns the fn and md values of a Google Cast TXT record: the name
// the device's owner gave it and its model. Either is empty when the record
// lacks it or cleanLabel refuses it.
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
// in b, and none when b is not a response or does not parse. Records past one
// that does not parse are dropped.
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

// Patterns dnssdName strips from an instance name, or refuses it for.
var (
	// machineID matches a run of hex long enough to be an ID, or a UUID, as
	// in "Android_0123456789abcdef0123456789abcdef".
	machineID = regexp.MustCompile(`(?i)[0-9a-f]{16}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

	// macPrefix and macSuffix match the hardware address AirPlay puts before
	// a speaker's name, as in "00005E005301@Kitchen", and Avahi puts after a
	// workstation's, as in "host-a [00:00:5e:00:53:01]".
	macPrefix = regexp.MustCompile(`^[0-9A-Fa-f]{12}@`)
	macSuffix = regexp.MustCompile(`\s*\[[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}\]$`)
)

// dnssdName returns the name to give a host that advertises services, and
// false when none of them gives one. The name a Google Cast device's owner
// gave it wins. Otherwise it is the instance name the most services share,
// once any hardware address is stripped, leaving out names that hold a
// machine ID. Two names shared equally go to the first in sort order, so the
// name is the same on every sweep.
func dnssdName(services []Service) (string, bool) {
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
		// Ties go to the earlier name, which MaxFunc keeps as the first
		// maximal element.
		return cmp.Compare(counts[a], counts[b])
	}), true
}
