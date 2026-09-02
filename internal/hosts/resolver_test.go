package hosts

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

// TestMain points the package resolver at one that can reach nothing, so no
// test here can quietly depend on the machine's own DNS. A test that wants an
// answer installs a stub of its own.
func TestMain(m *testing.M) {
	resolver = deadResolver()

	os.Exit(m.Run())
}

// deadResolver refuses every dial, which is how resolveName sees an address
// with no PTR record: an error, and so no name.
func deadResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("hosts: no resolver in tests")
		},
	}
}

// useResolver installs r for the duration of the test. The resolver is package
// state, so a test that swaps it must not be parallel.
func useResolver(t *testing.T, r *net.Resolver) {
	t.Helper()

	previous := resolver
	resolver = r

	t.Cleanup(func() { resolver = previous })
}

// countingResolver refuses every dial like deadResolver and counts the
// attempts, so a test can assert that no lookup was made at all.
func countingResolver() (*net.Resolver, *atomic.Int64) {
	var calls atomic.Int64

	r := &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			calls.Add(1)

			return nil, errors.New("hosts: no resolver in tests")
		},
	}

	return r, &calls
}

// stubResolver answers PTR queries from ptr, keyed by the fully qualified
// in-addr.arpa name, over a UDP socket on the loopback. An address the map does
// not hold answers NXDOMAIN, which is what a LAN resolver says about one it has
// no record for.
func stubResolver(t *testing.T, ptr map[string]string) *net.Resolver {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = pc.Close() })

	go serveDNS(pc, ptr)

	addr := pc.LocalAddr().String()

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, "udp", addr)
		},
	}
}

func serveDNS(pc net.PacketConn, ptr map[string]string) {
	buf := make([]byte, 512)

	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}

		reply, err := answerPTR(buf[:n], ptr)
		if err != nil {
			continue
		}

		_, _ = pc.WriteTo(reply, from)
	}
}

// answerPTR builds the response to one query. The reply is assembled with
// dnsmessage rather than pasted in as bytes, so the encoder cross-checks the
// parser the resolver runs and there is no golden binary to keep in step.
func answerPTR(query []byte, ptr map[string]string) ([]byte, error) {
	var p dnsmessage.Parser

	header, err := p.Start(query)
	if err != nil {
		return nil, err
	}

	question, err := p.Question()
	if err != nil {
		return nil, err
	}

	name, found := ptr[question.Name.String()]

	reply := dnsmessage.Header{
		ID:            header.ID,
		Response:      true,
		Authoritative: true,
		RCode:         dnsmessage.RCodeSuccess,
	}

	if !found || question.Type != dnsmessage.TypePTR {
		reply.RCode = dnsmessage.RCodeNameError
	}

	b := dnsmessage.NewBuilder(nil, reply)
	b.EnableCompression()

	if err := b.StartQuestions(); err != nil {
		return nil, err
	}

	if err := b.Question(question); err != nil {
		return nil, err
	}

	if err := b.StartAnswers(); err != nil {
		return nil, err
	}

	if reply.RCode == dnsmessage.RCodeSuccess {
		target, err := dnsmessage.NewName(name)
		if err != nil {
			return nil, err
		}

		resource := dnsmessage.ResourceHeader{
			Name:  question.Name,
			Type:  dnsmessage.TypePTR,
			Class: dnsmessage.ClassINET,
			TTL:   60,
		}

		if err := b.PTRResource(resource, dnsmessage.PTRResource{PTR: target}); err != nil {
			return nil, err
		}
	}

	return b.Finish()
}

func TestResolveNameTrimsTheTrailingDot(t *testing.T) {
	useResolver(t, stubResolver(t, map[string]string{
		"10.2.0.192.in-addr.arpa.": "printer.lan.",
	}))

	assert.Equal(t, "printer.lan", resolveName(t.Context(), netip.MustParseAddr("192.0.2.10")))
}

func TestResolveNameIsEmptyForAnAddressWithNoRecord(t *testing.T) {
	useResolver(t, stubResolver(t, map[string]string{
		"10.2.0.192.in-addr.arpa.": "printer.lan.",
	}))

	assert.Empty(t, resolveName(t.Context(), netip.MustParseAddr("192.0.2.11")))
}

func TestResolveNameIsEmptyWhenTheResolverCannotBeReached(t *testing.T) {
	assert.Empty(t, resolveName(t.Context(), netip.MustParseAddr("192.0.2.10")))
}

// A lookup must not outlive the caller. The sweep cancels its context on
// shutdown, and a resolution that ignored it would hold the poller open for the
// timeout on every outstanding address.
func TestResolveNameHonoursACancelledContext(t *testing.T) {
	useResolver(t, &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			<-ctx.Done()

			return nil, ctx.Err()
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	start := time.Now()

	assert.Empty(t, resolveName(ctx, netip.MustParseAddr("192.0.2.10")))
	assert.Less(t, time.Since(start), nameLookupTimeout,
		"a cancelled context should return before the lookup timeout")
}
