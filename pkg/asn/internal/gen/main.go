// Command gen builds the ASN tables embedded by package asn.
//
// The tables are generated and committed rather than fetched at run time so
// that lookups work on an isolated network, and so a build never depends on
// DB-IP being reachable.
//
// Unlike the OUI table they are written compressed. The source is thirty
// megabytes of CSV and even compacted the text is over thirteen; embedded as
// is, it would triple the size of the binary for a lookup most installs make
// only when traffic collection is on. A refresh therefore lands as an opaque
// blob, and the workflow that proposes it reports the counts instead.
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

// sourceURL is DB-IP's free IP-to-ASN table, published monthly under CC BY
// 4.0. The month is filled in by run.
const sourceURL = "https://download.db-ip.com/free/dbip-asn-lite-%s.csv.gz"

// minRanges guards against a fetch that succeeds but returns an error page:
// without it a truncated table would be committed and silently degrade every
// lookup. The real count is over 450,000.
const minRanges = 400_000

// userAgent identifies this generator to the source it fetches from.
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

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// A month's file appears some days into the month, so a run early in one
	// falls back to the last.
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

// load reads the source, which lists ranges in address order with IPv4 first.
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
// Only starts are stored: a lookup takes the last start at or below the
// address. Where the source leaves a gap, the address after a range's end is
// written with ASN 0, so an address nobody announces is not attributed to the
// range before it.
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

// writeOrgs writes one line per ASN, "asn<TAB>short<TAB>name". A short name
// identical to the full one is left empty; the reader restores it.
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

// legalSuffixes are trailing words that say what sort of company an
// organisation is rather than which one. Checked case-insensitively, repeatedly,
// so "Example Holdings, Inc." loses both.
var legalSuffixes = []string{
	"inc.", "inc", "llc", "l.l.c.", "ltd.", "ltd", "limited", "corporation",
	"corp.", "corp", "co.", "gmbh", "ag", "s.a.", "sa", "b.v.", "bv", "plc",
	"pty", "s.r.l.", "srl", "ab", "oy", "as", "a/s", "holdings", "company",
}

// shorten derives the name worth showing in a table cell: "Amazon.com, Inc."
// becomes "Amazon", "Google LLC" becomes "Google". It never shortens a name to
// nothing; a name made only of suffixes is kept whole.
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
