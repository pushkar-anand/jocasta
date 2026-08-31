// Command gen builds the OUI table embedded by package oui.
//
// The table is generated and committed rather than fetched at run time so that
// lookups work on an isolated network, and so a build never depends on IEEE or
// Wireshark being reachable.
//
// It is written uncompressed. Git deltas successive versions of a text file
// against each other, but must store a compressed one whole every time, and a
// refresh lands as a reviewable diff rather than an opaque binary blob.
package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// ieeeRegistries are the assignment registries, in the order they are merged.
// The width of an assignment is implied by its length, so the registry name
// itself is not needed once parsed.
var ieeeRegistries = []string{
	"https://standards-oui.ieee.org/oui/oui.csv",     // MA-L, 24-bit
	"https://standards-oui.ieee.org/oui28/mam.csv",   // MA-M, 28-bit
	"https://standards-oui.ieee.org/oui36/oui36.csv", // MA-S, 36-bit
	"https://standards-oui.ieee.org/cid/cid.csv",     // CID, 24-bit, not for globally unique use
}

// manufURL is Wireshark's curated table. IEEE publishes only legal entity
// names ("Apple, Inc."); this is the one public source of the abbreviated
// forms that belong in a table cell.
const manufURL = "https://www.wireshark.org/download/automated/data/manuf"

// minEntries guards against a fetch that succeeds but returns an error page:
// without it a truncated table would be committed and silently degrade every
// lookup. The real count is over 58,000.
const minEntries = 50_000

// userAgent identifies this generator to the registries it fetches from.
const userAgent = "jocasta-oui-generator/1 (+https://github.com/pushkar-anand/jocasta)"

type entry struct {
	short string
	long  string
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	table := make(map[string]*entry, 64_000)

	// IEEE is authoritative for which prefixes exist and for the full name.
	for _, u := range ieeeRegistries {
		if err := loadIEEE(ctx, u, table); err != nil {
			return fmt.Errorf("load %s: %w", u, err)
		}
	}

	// Wireshark then supplies short names, and any prefix IEEE has retired but
	// which is still seen in the wild.
	if err := loadManuf(ctx, table); err != nil {
		return fmt.Errorf("load %s: %w", manufURL, err)
	}

	if len(table) < minEntries {
		return fmt.Errorf("got %d entries, expected at least %d: a source is likely truncated", len(table), minEntries)
	}

	return write(table)
}

// fetch streams a URL to fn, which must consume the body before returning.
func fetch(ctx context.Context, url string, fn func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	// IEEE answers Go's default User-Agent with 418, so identify the tool.
	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	return fn(resp.Body)
}

func loadIEEE(ctx context.Context, url string, table map[string]*entry) error {
	return fetch(ctx, url, func(body io.Reader) error {
		r := csv.NewReader(body)
		// The address column is free text and contains stray quotes in a
		// handful of records, which a strict reader rejects.
		r.LazyQuotes = true
		r.FieldsPerRecord = -1

		for {
			rec, err := r.Read()
			if errors.Is(err, io.EOF) {
				return nil
			}

			if err != nil {
				return err
			}

			// Columns: Registry, Assignment, Organization Name, Address.
			if len(rec) < 3 || rec[0] == "Registry" {
				continue
			}

			key, ok := normalise(rec[1])
			if !ok {
				continue
			}

			name := clean(rec[2])
			if name == "" {
				continue
			}

			table[key] = &entry{long: name}
		}
	})
}

func loadManuf(ctx context.Context, table map[string]*entry) error {
	return fetch(ctx, manufURL, func(body io.Reader) error {
		raw, err := io.ReadAll(body)
		if err != nil {
			return err
		}

		for line := range strings.SplitSeq(string(raw), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			fields := strings.Split(line, "\t")
			if len(fields) < 2 {
				continue
			}

			key, ok := normalise(fields[0])
			if !ok {
				continue
			}

			short, long := clean(fields[1]), ""
			if len(fields) > 2 {
				long = clean(fields[2])
			}

			// A few records carry the prefix itself in the short column;
			// it is not a name, and displaying it would be worse than the
			// registered one.
			if strings.Contains(short, ":") {
				short = ""
			}

			e := table[key]
			if e == nil {
				e = &entry{}
				table[key] = e
			}

			e.short = short
			if e.long == "" {
				e.long = long
			}
		}

		return nil
	})
}

// normalise reduces an assignment or a manuf prefix to bare uppercase hex,
// truncated to the width the record declares.
//
// IEEE states the width by the length of the assignment; manuf states it with
// an explicit /bits suffix on anything longer than 24 bits.
func normalise(field string) (string, bool) {
	field = strings.TrimSpace(field)

	bits := 0

	if pfx, suffix, found := strings.Cut(field, "/"); found {
		field = pfx

		if _, err := fmt.Sscanf(suffix, "%d", &bits); err != nil {
			return "", false
		}
	}

	var sb strings.Builder
	sb.Grow(12)

	for _, r := range field {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F':
			sb.WriteRune(r)
		case r >= 'a' && r <= 'f':
			sb.WriteRune(r - 'a' + 'A')
		case r == ':' || r == '-' || r == '.':
		default:
			return "", false
		}
	}

	key := sb.String()
	if bits > 0 {
		if bits%4 != 0 || bits/4 > len(key) {
			return "", false
		}

		key = key[:bits/4]
	}

	// 24, 28 and 36 bits are the only assignment widths IEEE issues.
	switch len(key) {
	case 6, 7, 9:
		return key, true
	default:
		return "", false
	}
}

// clean strips the whitespace and embedded tabs that would corrupt the
// tab-separated table.
func clean(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\t", " "))
}

func write(table map[string]*entry) error {
	const out = "data.txt"

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriter(f)

	for _, key := range slices.Sorted(maps.Keys(table)) {
		e := table[key]

		// A short name identical to the full one is dropped; the reader
		// restores it, and the table is smaller for it.
		short := e.short
		if short == e.long {
			short = ""
		}

		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", key, short, e.long); err != nil {
			return err
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}

	log.Printf("wrote %s: %d entries", out, len(table))

	return f.Close()
}
