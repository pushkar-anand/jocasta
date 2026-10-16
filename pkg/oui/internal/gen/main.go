// Command gen builds the OUI table embedded by package oui.
//
// The table is generated and committed. So lookups work on an isolated
// network, and a build never depends on access to IEEE or Wireshark.
//
// The table is not compressed. Git stores each version of a text file as a
// delta to the version before. It stores a compressed file whole every time.
// Also, a refresh arrives as a diff that a person can review.
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

// ieeeRegistries are the assignment registries, in the order of the merge. The
// length of an assignment gives its width. So after the parse, the registry
// name is not necessary.
var ieeeRegistries = []string{
	"https://standards-oui.ieee.org/oui/oui.csv",     // MA-L, 24-bit
	"https://standards-oui.ieee.org/oui28/mam.csv",   // MA-M, 28-bit
	"https://standards-oui.ieee.org/oui36/oui36.csv", // MA-S, 36-bit
	"https://standards-oui.ieee.org/cid/cid.csv",     // CID, 24-bit, not for globally unique use
}

// manufURL is the curated table of Wireshark. IEEE publishes only legal entity
// names ("Apple, Inc."). This table is the only public source of the short
// forms for a table cell.
const manufURL = "https://www.wireshark.org/download/automated/data/manuf"

// minEntries guards against a fetch that succeeds but returns an error page.
// Without it, a truncated table could go into a commit, and every lookup
// would get worse without a warning. The real count is over 58,000.
const minEntries = 50_000

// userAgent identifies this generator to its registries.
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

// run downloads vendor assignments and short names. It then checks the
// minimum entry count, and replaces data.txt.
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	table := make(map[string]*entry, 64_000)

	// IEEE is the authority for which prefixes exist, and for the full name.
	for _, u := range ieeeRegistries {
		if err := loadIEEE(ctx, u, table); err != nil {
			return fmt.Errorf("load %s: %w", u, err)
		}
	}

	// Wireshark then gives short names. It also gives prefixes that IEEE
	// retired, but that devices still use.
	if err := loadManuf(ctx, table); err != nil {
		return fmt.Errorf("load %s: %w", manufURL, err)
	}

	if len(table) < minEntries {
		return fmt.Errorf("got %d entries, expected at least %d: a source is likely truncated", len(table), minEntries)
	}

	return write(table)
}

// fetch streams a URL to fn. fn must read the whole body before it returns.
func fetch(ctx context.Context, url string, fn func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	// IEEE answers 418 to the default User-Agent of Go. So identify the
	// tool.
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

// loadIEEE adds registered prefixes and names to table. It replaces duplicate
// prefixes. It skips rows without a usable prefix or name.
func loadIEEE(ctx context.Context, url string, table map[string]*entry) error {
	return fetch(ctx, url, func(body io.Reader) error {
		r := csv.NewReader(body)
		// The address column is free text. Some records have stray quotes in
		// it. A strict reader rejects these records.
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

			name := strings.TrimSpace(strings.ReplaceAll(rec[2], "\t", " "))
			if name == "" {
				continue
			}

			table[key] = &entry{long: name}
		}
	})
}

// loadManuf adds the short names and missing prefixes of Wireshark to table.
// It keeps the registered names from IEEE.
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

			short, long := strings.TrimSpace(strings.ReplaceAll(fields[1], "\t", " ")), ""
			if len(fields) > 2 {
				long = strings.TrimSpace(strings.ReplaceAll(fields[2], "\t", " "))
			}

			// Some records have the prefix itself in the short column. It is
			// not a name. The registered name is better to display.
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

// normalise changes an assignment or a manuf prefix to bare uppercase hex. It
// cuts it to the width that the record gives.
//
// IEEE gives the width by the length of the assignment. manuf gives it with
// a /bits suffix on each prefix longer than 24 bits.
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

// write replaces data.txt with vendor records in prefix order. If a write
// fails, it can leave a partial file.
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

		// If the short name is the same as the full name, drop it. The reader
		// restores it, and the table is smaller.
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
