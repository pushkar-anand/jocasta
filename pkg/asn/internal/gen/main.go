// Command gen builds the ASN tables embedded by package asn.
//
// The tables are generated and committed. So lookups work on an isolated
// network, and a build never depends on access to DB-IP.
//
// Unlike the OUI table, they are compressed. The source is thirty megabytes of
// CSV. Even in a compact form, the text is more than thirteen. Embedded as
// text, it would triple the size of the binary. Most installs use the lookup
// only when traffic collection is on. So a refresh arrives as an opaque blob,
// and the workflow that proposes it reports the counts.
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
	"maps"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// sourceURL is the free IP-to-ASN table of DB-IP. DB-IP publishes it each
// month under CC BY 4.0. run fills in the month.
const sourceURL = "https://download.db-ip.com/free/dbip-asn-lite-%s.csv.gz"

// minRanges guards against a fetch that succeeds but returns an error page.
// Without it, a truncated table could go into a commit, and every lookup
// would get worse without a warning. The real count is over 450,000.
const minRanges = 400_000

// userAgent identifies this generator to its source.
const userAgent = "jocasta-asn-generator/1 (+https://github.com/pushkar-anand/jocasta)"

type span struct {
	start, end netip.Addr
	asn        uint32
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run downloads the ASN data of this month, or of last month if this month
// has none. It replaces the embedded tables only if the data has the minimum
// range count.
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// The file of a month appears some days after the month starts. So a run
	// early in a month uses the file of the last month.
	now := time.Now().UTC()

	var (
		spans []span
		orgs  map[uint32]string
		err   error
	)

	for _, month := range []time.Time{now, now.AddDate(0, -1, 0)} {
		url := fmt.Sprintf(sourceURL, month.Format("2006-01"))

		spans, orgs, err = load(ctx, url)
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

	if err := writeRanges(spans); err != nil {
		return err
	}

	return writeOrgs(orgs)
}

// load reads the source. The source lists ranges in address order, with IPv4
// first.
func load(ctx context.Context, url string) ([]span, map[uint32]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("unexpected status %s", resp.Status)
	}

	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	r := csv.NewReader(zr)
	r.FieldsPerRecord = 4

	var spans []span

	orgs := make(map[uint32]string, 90_000)

	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return spans, orgs, nil
		}

		if err != nil {
			return nil, nil, err
		}

		start, err := netip.ParseAddr(rec[0])
		if err != nil {
			return nil, nil, err
		}

		end, err := netip.ParseAddr(rec[1])
		if err != nil {
			return nil, nil, err
		}

		n, err := strconv.ParseUint(rec[2], 10, 32)
		if err != nil {
			return nil, nil, err
		}

		asn := uint32(n)
		spans = append(spans, span{start: start, end: end, asn: asn})

		if name := clean(rec[3]); name != "" {
			orgs[asn] = name
		}
	}
}

// writeRanges writes one line per range start, "address<TAB>asn", sorted.
//
// The table stores only starts. A lookup takes the last start at or below the
// address. If the source has a gap, writeRanges writes the address after the
// end of a range with ASN 0. So an address that nobody announces does not go
// to the range before it.
func writeRanges(spans []span) error {
	slices.SortFunc(spans, func(a, b span) int { return a.start.Compare(b.start) })

	return writeGz("ranges.gz", func(w *bufio.Writer) (int, error) {
		n := 0

		for i, s := range spans {
			if _, err := fmt.Fprintf(w, "%s\t%d\n", s.start, s.asn); err != nil {
				return n, err
			}

			n++

			next := s.end.Next()
			if !next.IsValid() || next.Is4() != s.end.Is4() {
				continue
			}

			if i+1 < len(spans) && spans[i+1].start == next {
				continue
			}

			if _, err := fmt.Fprintf(w, "%s\t0\n", next); err != nil {
				return n, err
			}
		}

		return n, nil
	})
}

// writeOrgs writes one line per ASN, "asn<TAB>short<TAB>name". If the short
// name is the same as the full name, writeOrgs leaves it empty. The reader
// restores it.
func writeOrgs(orgs map[uint32]string) error {
	return writeGz("orgs.gz", func(w *bufio.Writer) (int, error) {
		n := 0

		for _, asn := range slices.Sorted(maps.Keys(orgs)) {
			name := orgs[asn]

			short := shorten(name)
			if short == name {
				short = ""
			}

			if _, err := fmt.Fprintf(w, "%d\t%s\t%s\n", asn, short, name); err != nil {
				return n, err
			}

			n++
		}

		return n, nil
	})
}

// writeGz replaces name with gzip data from fill, and logs its entry count. If
// a write fails, it can leave a partial file.
func writeGz(name string, fill func(*bufio.Writer) (int, error)) error {
	f, err := os.Create(name) //nolint:gosec // one of two constant names in this file.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(zw)

	n, err := fill(w)
	if err != nil {
		return err
	}

	if err := w.Flush(); err != nil {
		return err
	}

	if err := zw.Close(); err != nil {
		return err
	}

	log.Printf("wrote %s: %d entries", name, n)

	return f.Close()
}

// clean makes a registered name safe for a tab-separated line.
func clean(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// legalSuffixes are words at the end of a name that tell the kind of company.
// shorten checks them without case, again and again. So
// "Example Holdings, Inc." loses both suffixes.
var legalSuffixes = []string{
	"inc.", "inc", "llc", "l.l.c.", "ltd.", "ltd", "limited", "corporation",
	"corp.", "corp", "co.", "gmbh", "ag", "s.a.", "sa", "b.v.", "bv", "plc",
	"pty", "s.r.l.", "srl", "ab", "oy", "as", "a/s", "holdings", "company",
	"pvt.", "pvt", "pte.", "pte", "private", "co", "sdn", "bhd.", "bhd",
	"ltda.", "ltda",
}

// shorten makes the name to show in a table cell. "Amazon.com, Inc." becomes
// "Amazon", and "Google LLC" becomes "Google". It never shortens a name to
// nothing. It keeps a name that has only suffixes.
func shorten(name string) string {
	s := strings.TrimSpace(name)

	for {
		before := s
		s = strings.TrimRight(s, " ,")

		lower := strings.ToLower(s)
		for _, suf := range legalSuffixes {
			if strings.HasSuffix(lower, " "+suf) || strings.HasSuffix(lower, ","+suf) {
				s = s[:len(s)-len(suf)]

				break
			}
		}

		s = strings.TrimRight(s, " ,")
		if s == before {
			break
		}
	}

	s = strings.TrimSuffix(s, ".com")

	if s == "" {
		return name
	}

	return s
}
