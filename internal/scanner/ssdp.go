package scanner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"
	"golang.org/x/text/encoding/charmap"
)

// standardSSDPGroup is the multicast group and port an SSDP search is sent to,
// which the UPnP Device Architecture fixes.
var standardSSDPGroup = netip.MustParseAddrPort("239.255.255.250:1900")

// ssdpSearch asks every UPnP root device on the segment to answer. MX gives a
// device up to one second to do so.
var ssdpSearch = []byte("M-SEARCH * HTTP/1.1\r\n" +
	"HOST: 239.255.255.250:1900\r\n" +
	"MAN: \"ssdp:discover\"\r\n" +
	"MX: 1\r\n" +
	"ST: upnp:rootdevice\r\n" +
	"\r\n")

// ssdpWait is how long to read answers to a search: the one second MX allows,
// and askWait for the answer to arrive.
const ssdpWait = time.Second + askWait

// Limits on fetching a device description. The fetch is an HTTP request to an
// address a device named, so each one is bounded in size and time, and only a
// few run at once.
const (
	maxDescriptionSize = 64 << 10
	descriptionTimeout = 2 * time.Second
	descriptionFetches = 8
)

// maxLocationsPerHost caps how many description URLs are kept from one
// address. A host answers once for each UPnP root device it runs, such as a
// router with a gateway device and a media server, and a few is all a real
// host has.
const maxLocationsPerHost = 4

// maxLabelLength is the most characters a friendlyName may have. The UPnP
// Device Architecture asks for fewer than 64, and one more is let through.
const maxLabelLength = 64

// descriptionClient fetches device descriptions. It never uses a proxy, since
// the device is on the local network, and never follows a redirect, which
// could point anywhere.
var descriptionClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 16 << 10,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// askSSDP sends one SSDP search to group and returns the friendlyName of each
// address in addrs that answered with a description that has one. An address
// that did not answer, or answered with something unusable, is absent.
//
// It returns an error only when the search cannot be sent. When ctx ends
// first, it returns the names that arrived before then, with ctx's error.
func askSSDP(
	ctx context.Context,
	group netip.AddrPort,
	addrs []netip.Addr,
	wait time.Duration,
) (map[netip.Addr]string, error) {
	locations, err := searchSSDP(ctx, group, addrs, wait)
	if err != nil {
		return map[netip.Addr]string{}, err
	}

	var (
		mu    sync.Mutex
		names = make(map[netip.Addr]string, len(locations))
		g     errgroup.Group
	)

	g.SetLimit(descriptionFetches)

	for addr, locs := range locations {
		g.Go(func() error {
			// The URLs come sorted, so a host with more than one root device
			// is named after the same one on every sweep, and one that fails
			// to fetch falls through to the next.
			for _, loc := range locs {
				name, ok := fetchFriendlyName(ctx, loc)
				if !ok {
					continue
				}

				mu.Lock()
				names[addr] = name
				mu.Unlock()

				return nil
			}

			return nil
		})
	}

	// Each fetch records its own outcome and returns nil, so Wait has nothing
	// to report.
	_ = g.Wait()

	return names, ctx.Err()
}

// searchSSDP sends one search to group and returns, for each IPv4 address in
// addrs that answered, the usable description URLs its answers gave: at most
// maxLocationsPerHost, each once, sorted. It reads answers for the whole of
// wait, since a host answers once for each root device it runs, and returns
// an error only when the search cannot be sent.
func searchSSDP(
	ctx context.Context,
	group netip.AddrPort,
	addrs []netip.Addr,
	wait time.Duration,
) (map[netip.Addr][]*url.URL, error) {
	locations := make(map[netip.Addr][]*url.URL)

	asked := make(map[netip.Addr]bool, len(addrs))

	for _, a := range addrs {
		if a.Is4() {
			asked[a] = true
		}
	}

	if len(asked) == 0 {
		return locations, nil
	}

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return locations, fmt.Errorf("open SSDP socket: %w", err)
	}
	defer func() { _ = pc.Close() }()

	if _, err := pc.WriteTo(ssdpSearch, net.UDPAddrFromAddrPort(group)); err != nil {
		return locations, fmt.Errorf("send SSDP search: %w", err)
	}

	deadline := time.Now().Add(wait)
	if ctx.Err() != nil {
		deadline = time.Now()
	}

	_ = pc.SetReadDeadline(deadline)

	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()) })
	defer stop()

	buf := make([]byte, 2048)

	for {
		// The read deadline is what fails this read once the wait is over or
		// ctx ends.
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			break
		}

		udp, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}

		// Every device on the segment answers a search, so only the hosts
		// still without a name are kept.
		addr := udp.AddrPort().Addr().Unmap()
		if !asked[addr] {
			continue
		}

		if len(locations[addr]) >= maxLocationsPerHost {
			continue
		}

		loc, ok := parseSearchResponse(buf[:n])
		if !ok {
			continue
		}

		u, ok := descriptionURL(addr, loc)
		if !ok || slices.ContainsFunc(locations[addr], func(k *url.URL) bool { return k.String() == u.String() }) {
			continue
		}

		locations[addr] = append(locations[addr], u)
	}

	for _, locs := range locations {
		slices.SortFunc(locs, func(a, b *url.URL) int { return strings.Compare(a.String(), b.String()) })
	}

	return locations, nil
}

// parseSearchResponse returns the LOCATION header of the SSDP search response
// in b, and false when b is not a 200 response or has no LOCATION.
func parseSearchResponse(b []byte) (string, bool) {
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(b)), nil)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()

	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusOK || loc == "" {
		return "", false
	}

	return loc, true
}

// descriptionURL parses loc, the description URL a device at from gave, and
// reports whether it is an http URL on from's own address, with no user info.
// A device writes its own answer, so a URL on any other host is refused: a
// fetch goes only to the device that answered.
func descriptionURL(from netip.Addr, loc string) (*url.URL, bool) {
	u, err := url.Parse(loc)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return nil, false
	}

	host, err := netip.ParseAddr(u.Hostname())
	if err != nil || host.Unmap() != from {
		return nil, false
	}

	return u, true
}

// fetchFriendlyName fetches the device description at u and returns its
// friendlyName, and false when the fetch fails, is not a 200, runs past
// maxDescriptionSize or descriptionTimeout, or the description has no name
// cleanLabel passes.
func fetchFriendlyName(ctx context.Context, u *url.URL) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, descriptionTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false
	}

	resp, err := descriptionClient.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	// One byte past the limit tells a description that fills it from one that
	// runs over.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDescriptionSize+1))
	if err != nil || len(body) > maxDescriptionSize {
		return "", false
	}

	return parseDescription(body)
}

// description is the part of a UPnP device description a name is read from.
// The tags carry no namespace, so they match the device-1-0 namespace a
// description declares, or none.
type description struct {
	Device struct {
		FriendlyName string `xml:"friendlyName"`
	} `xml:"device"`
}

// parseDescription returns the root device's friendlyName from the
// description in b, and false when b does not parse or the name is one
// cleanLabel refuses. A description is read as UTF-8, or as ISO-8859-1 or
// windows-1252 when it declares one of those (see legacyCharset).
func parseDescription(b []byte) (string, bool) {
	var d description

	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.CharsetReader = legacyCharset

	if err := dec.Decode(&d); err != nil {
		return "", false
	}

	return cleanLabel(d.Device.FriendlyName)
}

// legacyCharset returns a reader that decodes input from the encoding label
// names. The UPnP Device Architecture asks for UTF-8, and the older devices
// that do not follow it declare ISO-8859-1 or windows-1252, so only those two
// are read. Any other label is an error, which fails the parse. Reading only
// these two keeps the tables for most other encodings out of the binary.
func legacyCharset(label string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "iso-8859-1", "iso8859-1", "latin1":
		return charmap.ISO8859_1.NewDecoder().Reader(input), nil
	case "windows-1252", "cp1252":
		return charmap.Windows1252.NewDecoder().Reader(input), nil
	default:
		return nil, fmt.Errorf("description in %q, which is not read", label)
	}
}

// cleanLabel collapses each run of white space in s to one space, trims the
// ends, and reports whether what is left is valid UTF-8 of 1 to 64 printable
// characters. A label is shown to a person, so it may hold spaces where a
// host name may not. A label it passes holds no control or format character,
// so a device cannot reverse how its label reads, as U+202E does, or hide
// characters in it to look like another device's.
func cleanLabel(s string) (string, bool) {
	if !utf8.ValidString(s) {
		return "", false
	}

	s = strings.Join(strings.Fields(s), " ")

	if s == "" || utf8.RuneCountInString(s) > maxLabelLength {
		return "", false
	}

	// IsPrint allows the ASCII space, which is the only space left.
	if strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return "", false
	}

	return s, true
}
