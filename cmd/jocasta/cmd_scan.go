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
}

func (s *ScanCmd) Run(ctx context.Context, log *slog.Logger) error {
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

	return outputScanResults(os.Stdout, hosts, s.JSON)
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
