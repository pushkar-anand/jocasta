// Command gen builds the IP-to-country table embedded by package geo.
//
// As the ASN tables, it is generated and committed. So lookups work on an
// isolated network, and a build never depends on access to DB-IP. It is also
// compressed, as they are. So a refresh arrives as an opaque blob, and the
// workflow that proposes it reports the counts.
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"
)

// sourceURL is the free IP-to-country table of DB-IP. DB-IP publishes it each
// month under CC BY 4.0. run fills in the month.
const sourceURL = "https://download.db-ip.com/free/dbip-country-lite-%s.csv.gz"

// minRanges guards against a fetch that succeeds but returns an error page.
// Without it, a truncated table could go into a commit, and every lookup
// would get worse without a warning. The real count is over 700,000.
const minRanges = 600_000

// userAgent identifies this generator to its source.
const userAgent = "jocasta-geo-generator/1 (+https://github.com/pushkar-anand/jocasta)"

type span struct {
	start, end netip.Addr
	country    string
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run downloads the country data of this month, or of last month if this
// month has none. It replaces the embedded table only if the data has the
// minimum range count.
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// The file of a month appears some days after the month starts. So a run
	// early in a month uses the file of the last month.
	now := time.Now().UTC()

	var (
		spans []span
		err   error
	)

	for _, month := range []time.Time{now, now.AddDate(0, -1, 0)} {
		url := fmt.Sprintf(sourceURL, month.Format("2006-01"))

		spans, err = load(ctx, url)
		if err == nil {
			log.Printf("read %s", url)

			break
		}

		log.Printf("load %s: %v", url, err)
	}

	if err != nil {
		return err
	}

	if len(spans) < minRanges {
		return fmt.Errorf("got %d ranges, expected at least %d: the source is likely truncated", len(spans), minRanges)
	}

	return writeRanges(spans)
}

// load reads the source. The source lists ranges in address order, with IPv4
// first.
func load(ctx context.Context, url string) ([]span, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}

	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}

	return parse(zr)
}

// parse reads "start,end,country" lines. ZZ is the DB-IP code for reserved
// and unallocated space, which no country holds. parse keeps it, so a lookup
// in that space misses. Without it, the address would go to the range
// before.
func parse(r io.Reader) ([]span, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = 3

	var spans []span

	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return spans, nil
		}

		if err != nil {
			return nil, err
		}

		start, err := netip.ParseAddr(rec[0])
		if err != nil {
			return nil, err
		}

		end, err := netip.ParseAddr(rec[1])
		if err != nil {
			return nil, err
		}

		country := strings.ToUpper(strings.TrimSpace(rec[2]))
		if len(country) != 2 {
			return nil, fmt.Errorf("range %s: country %q is not a two-letter code", rec[0], rec[2])
		}

		spans = append(spans, span{start: start, end: end, country: country})
	}
}

// writeRanges writes one line per range start, "address<TAB>country", sorted.
// The table stores only starts. A lookup takes the last start at or below the
// address. If the source has a gap, writeRanges writes the address after the
// end of a range as ZZ. It writes a series of ranges in one country once.
func writeRanges(spans []span) error {
	slices.SortFunc(spans, func(a, b span) int { return a.start.Compare(b.start) })

	f, err := os.Create("ranges.gz")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(zw)
	n := 0
	last := ""

	emit := func(a netip.Addr, country string) error {
		if country == last {
			return nil
		}

		last = country
		n++

		_, err := fmt.Fprintf(w, "%s\t%s\n", a, country)

		return err
	}

	for i, s := range spans {
		if i > 0 && s.start.Is4() != spans[i-1].start.Is4() {
			last = "" // the IPv6 table starts afresh.
		}

		if err := emit(s.start, s.country); err != nil {
			return err
		}

		next := s.end.Next()
		if !next.IsValid() || next.Is4() != s.end.Is4() {
			continue
		}

		if i+1 < len(spans) && spans[i+1].start == next {
			continue
		}

		if err := emit(next, "ZZ"); err != nil {
			return err
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}

	if err := zw.Close(); err != nil {
		return err
	}

	log.Printf("wrote ranges.gz: %d entries", n)

	return f.Close()
}
