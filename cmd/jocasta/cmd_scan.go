package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"text/tabwriter"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

type ScanCmd struct {
	Target       string        `arg:"" help:"Target CIDR prefix to sweep (e.g. 192.168.1.0/24)."`
	Rate         int           `name:"rate" help:"Maximum ICMP echo probes per second." default:"1000"`
	Rounds       int           `name:"rounds" help:"Probes per address before declaring host down." default:"2"`
	Wait         time.Duration `name:"wait" help:"Wait duration for replies after final probe." default:"2s"`
	ResolveNames bool          `name:"resolve-names" negatable:"" help:"Resolve hostnames via reverse DNS." default:"true"`
	ResolveMACs  bool          `name:"resolve-macs" negatable:"" help:"Resolve MAC addresses via neighbour table." default:"true"`
	JSON         bool          `name:"json" help:"Output results as JSON."`
	Save         bool          `name:"save" help:"Record the results in the device inventory."`
	Source       string        `name:"source" help:"Name recorded as the origin of these results. Defaults to this host's name."`
}

func (s *ScanCmd) Run(ctx context.Context, cfg *Config, log *slog.Logger, conn *db.DB) error {
	p, err := netip.ParsePrefix(s.Target)
	if err != nil {
		return fmt.Errorf("invalid CIDR prefix %q: %w", s.Target, err)
	}

	opts := []scanner.Option{
		scanner.WithRate(s.Rate),
		scanner.WithRounds(s.Rounds),
		scanner.WithWait(s.Wait),
		scanner.WithNameResolution(s.ResolveNames),
		scanner.WithMACResolution(s.ResolveMACs),
	}

	sc := scanner.New(log, opts...)
	hosts, err := sc.Scan(ctx, p)
	if err != nil {
		return fmt.Errorf("scan %s: %w", p, err)
	}

	if err := outputScanResults(os.Stdout, hosts, s.JSON); err != nil {
		return err
	}

	if !s.Save {
		return nil
	}

	return s.save(ctx, cfg, log, p, hosts, conn)
}

// save records the sweep in the inventory. It runs after the results are
// printed so a database that will not open still leaves the operator with the
// scan they asked for.
func (s *ScanCmd) save(ctx context.Context, cfg *Config, log *slog.Logger, p netip.Prefix, hosts []scanner.Host, conn *db.DB) error {
	res, err := inventory.New(conn.Conn, log).RecordSweep(ctx, s.sourceName(), p, hosts)
	if err != nil {
		return fmt.Errorf("record sweep: %w", err)
	}

	log.InfoContext(ctx, "recorded sweep",
		"scan", res.ScanID,
		slog.Int("seen", res.Seen),
		slog.Int("discovered", res.Discovered),
		slog.Int("identified", res.Identified),
		slog.Int("merged", res.Merged),
	)

	return nil
}

// sourceName identifies which scanner produced these results, so a second one
// on another segment stays distinguishable in the provenance.
func (s *ScanCmd) sourceName() string {
	if s.Source != "" {
		return s.Source
	}

	name, err := os.Hostname()
	if err != nil {
		return "sweep"
	}

	return "sweep:" + name
}

func outputScanResults(w io.Writer, hosts []scanner.Host, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(hosts)
	}

	if len(hosts) == 0 {
		_, err := fmt.Fprintln(w, "No responsive hosts found.")
		return err
	}

	// tabwriter holds every row until Flush, so the single check there reports
	// any write failure from the rows below.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "IP\tMAC\tVENDOR\tHOSTNAME\tRTT\tDETAILS")

	for _, h := range hosts {
		vendor := cmp.Or(h.Vendor, "-")
		if h.Vendor == "" && h.Randomised {
			vendor = "[randomised]"
		}

		details := ""
		if h.Self {
			details = "self"
			if h.Interface != "" {
				details = fmt.Sprintf("self (%s)", h.Interface)
			}
		}

		// Truncate alone renders anything under a microsecond as "0s".
		rtt := h.RTT.Truncate(time.Microsecond).String()
		if h.RTT < time.Microsecond {
			rtt = "<1µs"
		}

		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			h.Addr, cmp.Or(h.MAC, "-"), vendor, cmp.Or(h.Hostname, "-"), rtt, details,
		)
	}

	return tw.Flush()
}
