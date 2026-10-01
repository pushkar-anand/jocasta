package scanner

import (
	"fmt"
	"net/netip"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

// standardMDNSPort is where an mDNS responder listens.
const standardMDNSPort = 5353

// mdns asks a host for its name with an mDNS reverse lookup sent to the host
// itself (RFC 6762, section 5.5).
var mdns = nameProtocol{
	query: func(addr netip.Addr) ([]byte, error) {
		return ptrQuery(reverseName(addr))
	},
	answer: func(b []byte, addr netip.Addr) (string, bool) {
		return parseReverseAnswer(b, reverseName(addr))
	},
}

// reverseName returns the in-addr.arpa name that a reverse lookup of addr
// asks about. addr must be an IPv4 address.
func reverseName(addr netip.Addr) string {
	b := addr.As4()

	return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", b[3], b[2], b[1], b[0])
}

// ptrQuery returns a query with a PTR question for each of names, and message
// ID 0, which parseReverseAnswer relies on to ignore the ID.
func ptrQuery(names ...string) ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})

	if err := b.StartQuestions(); err != nil {
		return nil, err
	}

	for _, name := range names {
		n, err := dnsmessage.NewName(name)
		if err != nil {
			return nil, err
		}

		if err := b.Question(dnsmessage.Question{Name: n, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}); err != nil {
			return nil, err
		}
	}

	return b.Finish()
}

// parseReverseAnswer returns the name that the response in b gives for the
// reverse name q. It returns false when b is not a DNS response, has no PTR
// answer for q, or gives a name cleanName refuses.
func parseReverseAnswer(b []byte, q string) (string, bool) {
	var p dnsmessage.Parser

	// The message ID is left unchecked. A responder may echo the query's or
	// send zero, and the query sends zero, so the ID tells nothing apart.
	h, err := p.Start(b)
	if err != nil || !h.Response {
		return "", false
	}

	if err := p.SkipAllQuestions(); err != nil {
		return "", false
	}

	for {
		ah, err := p.AnswerHeader()
		if err != nil {
			return "", false
		}

		if ah.Type != dnsmessage.TypePTR || !strings.EqualFold(ah.Name.String(), q) {
			if err := p.SkipAnswer(); err != nil {
				return "", false
			}

			continue
		}

		ptr, err := p.PTRResource()
		if err != nil {
			return "", false
		}

		return cleanName(ptr.PTR.String())
	}
}
