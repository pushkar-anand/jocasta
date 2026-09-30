package scanner

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

// questionName returns the name the DNS query in b asks about.
func questionName(b []byte) (string, bool) {
	var p dnsmessage.Parser

	if _, err := p.Start(b); err != nil {
		return "", false
	}

	q, err := p.Question()
	if err != nil {
		return "", false
	}

	return q.Name.String(), true
}

// ptrResponse builds a response answering the reverse name q with a PTR to
// name.
func ptrResponse(q, name string) ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})

	if err := b.StartAnswers(); err != nil {
		return nil, err
	}

	err := b.PTRResource(
		dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(q), Class: dnsmessage.ClassINET, TTL: 120},
		dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(name)},
	)
	if err != nil {
		return nil, err
	}

	return b.Finish()
}

func TestReverseName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "10.2.0.192.in-addr.arpa.", reverseName(netip.MustParseAddr("192.0.2.10")))
}

func TestParseReverseAnswer(t *testing.T) {
	t.Parallel()

	const q = "10.2.0.192.in-addr.arpa."

	// answer builds a response with one record of type typ for name.
	answer := func(t *testing.T, name string, typ dnsmessage.Type, ptr string) []byte {
		t.Helper()

		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
		require.NoError(t, b.StartAnswers())

		h := dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Class: dnsmessage.ClassINET}

		switch typ {
		case dnsmessage.TypePTR:
			require.NoError(t, b.PTRResource(h, dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(ptr)}))
		case dnsmessage.TypeA:
			require.NoError(t, b.AResource(h, dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}}))
		}

		raw, err := b.Finish()
		require.NoError(t, err)

		return raw
	}

	query, err := reverseQuery(q)
	require.NoError(t, err)

	tests := []struct {
		name   string
		msg    []byte
		want   string
		wantOK bool
	}{
		{name: "a PTR for the asked name", msg: answer(t, q, dnsmessage.TypePTR, "tv.local."), want: "tv.local", wantOK: true},
		{name: "the asked name in another case", msg: answer(t, "10.2.0.192.IN-ADDR.ARPA.", dnsmessage.TypePTR, "tv.local."), want: "tv.local", wantOK: true},
		{name: "a PTR for another address", msg: answer(t, "11.2.0.192.in-addr.arpa.", dnsmessage.TypePTR, "tv.local.")},
		{name: "an A record only", msg: answer(t, q, dnsmessage.TypeA, "")},
		{name: "a name with a space", msg: answer(t, q, dnsmessage.TypePTR, "living room.local.")},
		{name: "a name with a control character", msg: answer(t, q, dnsmessage.TypePTR, "tv\x07.local.")},
		{name: "the root name", msg: answer(t, q, dnsmessage.TypePTR, ".")},
		{name: "a query", msg: query},
		{name: "a truncated message", msg: answer(t, q, dnsmessage.TypePTR, "tv.local.")[:20]},
		{name: "nothing", msg: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseReverseAnswer(tt.msg, q)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
