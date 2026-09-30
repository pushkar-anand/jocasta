package scanner

import (
	"encoding/binary"
	"net/netip"
	"strings"
)

// standardNetBIOSPort is where a NetBIOS name service listens.
const standardNetBIOSPort = 137

// netbios asks a host for its name with a NetBIOS node status request (RFC
// 1002, section 4.2.17), which Windows and Samba answer.
var netbios = nameProtocol{
	query:  func(netip.Addr) ([]byte, error) { return nodeStatusQuery, nil },
	answer: func(b []byte, _ netip.Addr) (string, bool) { return parseNodeStatus(b) },
}

// NetBIOS node status fields (RFC 1002, sections 4.2.1 and 4.2.18).
const (
	nbHeaderLen     = 12
	nbTypeNBSTAT    = 0x0021
	nbFlagResponse  = 0x8000
	nbNameGroup     = 0x8000
	nbNameEntryLen  = 18
	nbNameLen       = 15
	nbSuffixMachine = 0x00
)

// nodeStatusQuery asks a host for every name it holds. It asks about the name
// "*", as nbtstat -A does, first-level encoded as CK and 30 As. Its
// transaction ID is 0, and parseNodeStatus ignores the ID, because only the
// asked host can answer from the port it was asked on.
var nodeStatusQuery = []byte{
	0x00, 0x00, // transaction ID
	0x00, 0x00, // flags: a query
	0x00, 0x01, // one question
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // no answer, authority or additional records
	0x20, // a 32-byte label
	'C', 'K', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A',
	'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A', 'A',
	0x00,       // the end of the name
	0x00, 0x21, // type NBSTAT
	0x00, 0x01, // class IN
}

// parseNodeStatus returns the host's own name from the node status response
// in b: the first unique name with suffix 0x00, with its padding trimmed. It
// returns false when b is not a node status response, lists no such name, or
// the name is one cleanName refuses.
func parseNodeStatus(b []byte) (string, bool) {
	if len(b) < nbHeaderLen || binary.BigEndian.Uint16(b[2:])&nbFlagResponse == 0 {
		return "", false
	}

	if binary.BigEndian.Uint16(b[6:]) == 0 {
		return "", false
	}

	off, ok := skipName(b, nbHeaderLen)
	if !ok {
		return "", false
	}

	// Type, class, TTL and the length of the data, then the data itself.
	if len(b) < off+10 || binary.BigEndian.Uint16(b[off:]) != nbTypeNBSTAT {
		return "", false
	}

	rdLen := int(binary.BigEndian.Uint16(b[off+8:]))
	off += 10

	if len(b) < off+rdLen || rdLen < 1 {
		return "", false
	}

	data := b[off : off+rdLen]
	count := int(data[0])
	entries := data[1:]

	for i := range count {
		start := i * nbNameEntryLen
		if len(entries) < start+nbNameEntryLen {
			return "", false
		}

		// A host also lists its workgroup with suffix 0x00, marked as a group
		// name, so the group flag tells the two apart.
		e := entries[start : start+nbNameEntryLen]
		if e[nbNameLen] != nbSuffixMachine || binary.BigEndian.Uint16(e[nbNameLen+1:])&nbNameGroup != 0 {
			continue
		}

		return cleanName(strings.TrimRight(string(e[:nbNameLen]), " \x00"))
	}

	return "", false
}

// skipName returns the offset just past the encoded name that starts at off
// in b, and false when the name runs past b.
func skipName(b []byte, off int) (int, bool) {
	for off < len(b) {
		n := int(b[off])

		switch {
		case n == 0:
			return off + 1, true
		case n&0xc0 == 0xc0:
			// A pointer to an earlier name ends this one in two bytes.
			return off + 2, off+2 <= len(b)
		default:
			off += 1 + n
		}
	}

	return 0, false
}
