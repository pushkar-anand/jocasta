package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/pkg/cidr"
)

type PortsCmd struct {
	Target      string        `arg:"" help:"Address or CIDR prefix to probe (e.g. 192.0.2.10 or 192.0.2.0/24)."`
	Ports       string        `name:"ports" help:"Ports to probe, as a spec like '22,80,443,8000-8100'. Defaults to a curated preset."`
	Timeout     time.Duration `name:"timeout" help:"Per-connection dial timeout." default:"500ms"`
	Concurrency int           `name:"concurrency" help:"Maximum connections in flight." default:"256"`
	JSON        bool          `name:"json" help:"Output results as JSON."`
}

// Run probes an address or a prefix and prints what answered. It is the way a
// port scan is checked against real hardware without starting the server, the
// same affordance scan and plugin provide; recording what it finds in the
// inventory comes with the --save flag a later step adds.
func (p *PortsCmd) Run(ctx context.Context, log *slog.Logger) error {
	targets, err := portTargets(p.Target)
	if err != nil {
		return err
	}

	opts := []scanner.PortOption{
		scanner.WithDialTimeout(p.Timeout),
		scanner.WithConcurrency(p.Concurrency),
	}

	if p.Ports != "" {
		ports, err := scanner.ParsePortSpec(p.Ports)
		if err != nil {
			return err
		}

		opts = append(opts, scanner.WithPorts(ports))
	}

	ps := scanner.NewPortScanner(log, opts...)

	results := ps.Scan(ctx, targets, time.Now())

	return outputPortScans(os.Stdout, results, p.JSON)
}

// portTargets reads the command's target as either a single address or a
// prefix to expand.
func portTargets(target string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(target); err == nil {
		return []netip.Addr{addr}, nil
	}

	prefix, err := netip.ParsePrefix(target)
	if err != nil {
		return nil, fmt.Errorf("target %q is neither an address nor a CIDR prefix", target)
	}

	seq, err := cidr.Hosts(prefix)
	if err != nil {
		return nil, err
	}

	return slices.Collect(seq), nil
}

func outputPortScans(w io.Writer, results []scanner.PortScan, asJSON bool) error {
	if asJSON {
		return writeJSON(w, results)
	}

	var open int
	for _, r := range results {
		open += len(r.Open)
	}

	if open == 0 {
		_, err := fmt.Fprintln(w, "No open ports found.")

		return err
	}

	// tabwriter holds every row until Flush, so the single check there reports
	// any write failure from the rows below.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ADDRESS\tPORT\tSERVICE")

	for _, r := range results {
		for _, port := range r.Open {
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\n",
				r.Addr, port, cmp.Or(scanner.ServiceName(port), "-"))
		}
	}

	return tw.Flush()
}
