package main

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/pkg/cidr"
)

type PortsCmd struct {
	Target      string        `arg:"" optional:"" help:"Address or CIDR prefix to probe. Defaults to every current address in the inventory."`
	Ports       string        `name:"ports" help:"Ports to probe, as a spec like '22,80,443,8000-8100'. Defaults to a curated preset."`
	Timeout     time.Duration `name:"timeout" help:"Per-connection dial timeout." default:"500ms"`
	Concurrency int           `name:"concurrency" help:"Maximum connections in flight." default:"256"`
	JSON        bool          `name:"json" help:"Output results as JSON."`
	Save        bool          `name:"save" help:"Record the open ports in the device inventory."`
}

// Run probes an address, a prefix, or every address the inventory holds, and
// prints what answered. It is the way a port scan is checked against real
// hardware without starting the server, the same affordance scan and plugin
// provide.
func (p *PortsCmd) Run(ctx context.Context, cfg *config.Config, log *slog.Logger, conn *sql.DB) error {
	store := inventory.New(conn, log)

	targets, err := p.targets(ctx, store)
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

	results := scanner.NewPortScanner(log, opts...).Scan(ctx, targets, time.Now())

	if err := outputPortScans(os.Stdout, results, p.JSON); err != nil {
		return err
	}

	if !p.Save {
		return nil
	}

	return p.save(ctx, cfg, log, store, results)
}

// targets resolves what to scan: an explicit address or prefix, or every
// current address in the inventory when none was given.
func (p *PortsCmd) targets(ctx context.Context, store *inventory.Store) ([]netip.Addr, error) {
	if p.Target == "" {
		targets, err := store.PortScanTargets(ctx)
		if err != nil {
			return nil, err
		}

		if len(targets) == 0 {
			return nil, fmt.Errorf("the inventory holds no addresses to scan; run a discovery sweep first")
		}

		return targets, nil
	}

	return portTargets(p.Target)
}

// save records the scan in the inventory. It runs after the results are printed
// so a database that will not open still leaves the operator with the scan they
// asked for.
func (p *PortsCmd) save(
	ctx context.Context,
	cfg *config.Config,
	log *slog.Logger,
	store *inventory.Store,
	results []scanner.PortScan,
) error {
	sum, err := store.RecordPorts(ctx, cfg.Scan.Source, results)
	if err != nil {
		return fmt.Errorf("record ports: %w", err)
	}

	log.InfoContext(ctx, "recorded port scan",
		slog.Int64("scan", sum.ScanID),
		slog.Int("devices", sum.Devices),
		slog.Int("dropped", sum.Dropped),
		slog.Int("open", sum.Open),
		slog.Int("opened", sum.Opened),
		slog.Int("closed", sum.Closed),
	)

	return nil
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
