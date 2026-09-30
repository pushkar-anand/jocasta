package scanner

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nbName is one entry in a node status response's name list.
type nbName struct {
	name   string
	suffix byte
	group  bool
}

// nodeStatusResponse builds a node status response listing names, in the
// shape Windows and Samba send: the full encoded name, then the name list,
// then 46 bytes of statistics.
func nodeStatusResponse(names ...nbName) []byte {
	return nodeStatusResponseNamed(nodeStatusQuery[nbHeaderLen:nbHeaderLen+34], names...)
}

// nodeStatusResponseNamed builds a node status response whose answer carries
// the encoded name rrName.
func nodeStatusResponseNamed(rrName []byte, names ...nbName) []byte {
	data := []byte{byte(len(names))} //nolint:gosec // G115: a test lists a handful of names.

	for _, n := range names {
		entry := make([]byte, nbNameEntryLen)
		copy(entry, []byte(n.name + "               ")[:nbNameLen])
		entry[nbNameLen] = n.suffix

		if n.group {
			binary.BigEndian.PutUint16(entry[nbNameLen+1:], nbNameGroup)
		}

		data = append(data, entry...)
	}

	// The statistics start with the unit ID, a MAC address.
	data = append(data, make([]byte, 46)...)
	copy(data[len(data)-46:], []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x01})

	b := []byte{
		0x00, 0x00, // transaction ID
		0x84, 0x00, // flags: an authoritative response
		0x00, 0x00, // no questions
		0x00, 0x01, // one answer
		0x00, 0x00, 0x00, 0x00, // no authority or additional records
	}
	b = append(b, rrName...)
	b = append(b, 0x00, 0x21, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00) // type, class, TTL
	b = binary.BigEndian.AppendUint16(b, uint16(len(data)))       //nolint:gosec // G115: a few hundred bytes at most.

	return append(b, data...)
}

func TestNodeStatusQueryAsksForEveryName(t *testing.T) {
	t.Parallel()

	require.Len(t, nodeStatusQuery, 50)

	// First-level encoding writes each byte as two letters, 'A' plus each
	// half. The asked name is "*" padded with 15 zero bytes.
	label := nodeStatusQuery[nbHeaderLen+1 : nbHeaderLen+33]

	decoded := make([]byte, 16)
	for i := range decoded {
		decoded[i] = (label[2*i]-'A')<<4 | (label[2*i+1] - 'A')
	}

	assert.Equal(t, append([]byte{'*'}, make([]byte, 15)...), decoded)
	assert.Equal(t, uint16(nbTypeNBSTAT), binary.BigEndian.Uint16(nodeStatusQuery[46:]))
}

func TestParseNodeStatus(t *testing.T) {
	t.Parallel()

	workgroup := nbName{"WORKGROUP", nbSuffixMachine, true}
	machine := nbName{"DESKTOP-4F2K", nbSuffixMachine, false}
	server := nbName{"DESKTOP-4F2K", 0x20, false}

	notResponse := nodeStatusResponse(machine)
	notResponse[2] = 0x00

	noAnswers := nodeStatusResponse(machine)
	noAnswers[7] = 0x00

	wrongType := nodeStatusResponse(machine)
	wrongType[nbHeaderLen+34+1] = 0x20

	// Past the one real entry, the parser reads the statistics as names, and
	// cleanName refuses the first one.
	overCount := nodeStatusResponse(workgroup)
	overCount[nbHeaderLen+34+10] = 200

	tests := []struct {
		name   string
		msg    []byte
		want   string
		wantOK bool
	}{
		{name: "the machine name after the workgroup", msg: nodeStatusResponse(workgroup, machine, server), want: "DESKTOP-4F2K", wantOK: true},
		{name: "an answer that points to its name", msg: nodeStatusResponseNamed([]byte{0xc0, 0x0c}, machine), want: "DESKTOP-4F2K", wantOK: true},
		{name: "a name that fills all 15 bytes", msg: nodeStatusResponse(nbName{"ABCDEFGHIJKLMNO", nbSuffixMachine, false}), want: "ABCDEFGHIJKLMNO", wantOK: true},
		{name: "the workgroup alone", msg: nodeStatusResponse(workgroup)},
		{name: "a server name alone", msg: nodeStatusResponse(server)},
		{name: "a name with a space inside", msg: nodeStatusResponse(nbName{"LIVING ROOM", nbSuffixMachine, false})},
		{name: "an empty list", msg: nodeStatusResponse()},
		{name: "a query", msg: nodeStatusQuery},
		{name: "a message that is no response", msg: notResponse},
		{name: "a response with no answer", msg: noAnswers},
		{name: "an answer of another type", msg: wrongType},
		{name: "more names counted than sent", msg: overCount},
		{name: "nothing", msg: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseNodeStatus(tt.msg)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// A host writes its own answer, so a response cut short anywhere must be
// refused, never read past its end.
func TestParseNodeStatusRefusesEveryTruncation(t *testing.T) {
	t.Parallel()

	full := nodeStatusResponse(nbName{"DESKTOP-4F2K", nbSuffixMachine, false})

	for n := range len(full) {
		_, ok := parseNodeStatus(full[:n])
		assert.False(t, ok, "cut to %d bytes", n)
	}
}

func TestAskNamesOverNetBIOS(t *testing.T) {
	t.Parallel()

	r := newResponder(t, "127.0.0.1:0", nil, netbiosReply("DESKTOP-4F2K"))

	names, err := askNames(t.Context(), netbios, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, r.port(), 1000, time.Second)
	require.NoError(t, err)

	assert.Equal(t, map[netip.Addr]string{netip.MustParseAddr("127.0.0.1"): "DESKTOP-4F2K"}, names)
}
