package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

type PluginCmd struct {
	Run PluginRunCmd `cmd:"" help:"Read one configured source and print what it claims."`
}

type PluginRunCmd struct {
	Name string `arg:"" help:"Instance name of the source to read, as it appears under plugins."`
	JSON bool   `name:"json" help:"Output facts as JSON."`
	Save bool   `name:"save" help:"Record the facts in the device inventory."`
}

// Run reads one source without starting the server, which is how a credential
// or a firewall rule is checked against the real thing.
//
// It reads the source even when it is disabled in config: an operator naming an
// instance explicitly is asking about that instance, and having to enable it
// first would mean editing config to find out whether the config is right.
func (p *PluginRunCmd) Run(
	ctx context.Context,
	cfg *config.Config,
	log *slog.Logger,
	store *inventory.Store,
) error {
	rc, ok := cfg.Plugins.RouterOS[p.Name]
	if !ok {
		return fmt.Errorf("no source named %q is configured", p.Name)
	}

	src, err := newRouterOS(p.Name, rc, log)
	if err != nil {
		return err
	}

	if src.IsTopologyOnly() {
		return p.runTopology(ctx, log, src)
	}

	nets, err := src.Networks(ctx)
	if err != nil {
		// The segments only decorate the devices, so a source that will not
		// describe them is still worth reading.
		log.WarnContext(ctx, "source did not describe its segments",
			slog.String("src", src.Name()),
			logger.Err(err),
		)
	}

	facts, err := src.Discover(ctx)

	// A half-read source still has something to show, so the error is reported
	// alongside the rows.
	if err != nil && len(facts) == 0 {
		return fmt.Errorf("discover %s: %w", src.Name(), err)
	}

	if err != nil {
		log.WarnContext(ctx, "source answered in part",
			slog.String("src", src.Name()),
			slog.Int("facts", len(facts)),
			logger.Err(err),
		)
	}

	if err := outputNetworks(os.Stdout, nets, p.JSON); err != nil {
		return err
	}

	if err := outputFacts(os.Stdout, facts, p.JSON); err != nil {
		return err
	}

	if err := p.printTopology(ctx, log, src); err != nil {
		return err
	}

	if !p.Save {
		return nil
	}

	return p.save(ctx, log, src, nets, facts, store)
}

// runTopology reads a switch or access point, which has no devices or
// segments of its own to report.
func (p *PluginRunCmd) runTopology(ctx context.Context, log *slog.Logger, src plugin.TopologyReader) error {
	topo, err := src.Topology(ctx)
	if err != nil && len(topo.Seen) == 0 {
		return fmt.Errorf("read topology from %s: %w", src.Name(), err)
	}

	if err != nil {
		log.WarnContext(ctx, "source answered in part", slog.String("src", src.Name()), logger.Err(err))
	}

	return outputTopology(os.Stdout, topo, p.JSON)
}

// printTopology reads and prints what is plugged into a router read for its
// devices as well. The devices are the point of that read, so a topology that
// will not come back is only logged.
func (p *PluginRunCmd) printTopology(ctx context.Context, log *slog.Logger, src plugin.TopologyReader) error {
	topo, err := src.Topology(ctx)
	if err != nil {
		log.WarnContext(ctx, "source did not say what is plugged into it",
			slog.String("src", src.Name()),
			logger.Err(err),
		)

		if len(topo.Seen) == 0 {
			return nil
		}
	}

	return outputTopology(os.Stdout, topo, p.JSON)
}

// save records the reading. It runs after the facts are printed so a database
// that will not open still leaves the operator with the read they asked for.
func (p *PluginRunCmd) save(
	ctx context.Context,
	log *slog.Logger,
	src plugin.HostDiscoverer,
	nets []plugin.Network,
	facts []plugin.Fact,
	store *inventory.Store,
) error {
	// Segments first, for the same reason the poller does it in that order: an
	// address is matched to the networks already recorded.
	if err := store.RecordNetworks(ctx, nets); err != nil {
		return fmt.Errorf("record networks: %w", err)
	}

	res, err := store.RecordFacts(ctx, src.Name(), src.Kind(), facts)
	if err != nil {
		return fmt.Errorf("record facts: %w", err)
	}

	log.InfoContext(ctx, "recorded source",
		slog.String("src", src.Name()),
		slog.Int64("scan", res.ScanID),
		slog.Int("seen", res.Seen),
		slog.Int("discovered", res.Discovered),
		slog.Int("identified", res.Identified),
		slog.Int("merged", res.Merged),
		slog.Int("dropped", res.Dropped),
	)

	return nil
}

// outputNetworks prints the segments the source described. Under --json the
// two tables are separate documents, so a reader piping this to jq gets the
// networks and the facts with no wrapper around them.
func outputNetworks(w io.Writer, nets []plugin.Network, asJSON bool) error {
	if asJSON {
		return writeJSON(w, nets)
	}

	if len(nets) == 0 {
		_, err := fmt.Fprintln(w, "Source described no segments.")

		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NETWORK\tVLAN\tNAME")

	for _, n := range nets {
		vlan := "-"
		if n.VLAN != 0 {
			vlan = strconv.Itoa(n.VLAN)
		}

		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", n.Prefix, vlan, cmp.Or(n.Name, "-"))
	}

	if err := tw.Flush(); err != nil {
		return err
	}

	_, err := fmt.Fprintln(w)

	return err
}

func outputFacts(w io.Writer, facts []plugin.Fact, asJSON bool) error {
	if asJSON {
		return writeJSON(w, facts)
	}

	if len(facts) == 0 {
		_, err := fmt.Fprintln(w, "Source reported no devices.")

		return err
	}

	// tabwriter holds every row until Flush, so the single check there reports
	// any write failure from the rows below.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ADDRESS\tMAC\tVENDOR\tHOSTNAME\tSTANDING\tPRESENT")

	for _, f := range facts {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%t\n",
			f.Host.Address(),
			cmp.Or(f.Host.MAC, "-"),
			cmp.Or(f.Host.ShortName(), "-"),
			cmp.Or(f.Host.Hostname(), "-"),
			cmp.Or(string(f.HostnameSource), "-"),
			f.Present,
		)
	}

	return tw.Flush()
}

// outputTopology prints what a source said is plugged into it: its ports, the
// addresses learned on each, and its neighbours.
func outputTopology(w io.Writer, t plugin.Topology, asJSON bool) error {
	if asJSON {
		return writeJSON(w, t)
	}

	role := "switch or access point"
	if t.Gateway {
		role = "gateway"
	}

	_, _ = fmt.Fprintf(w, "%s (%s)\n\n", cmp.Or(t.Identity, "unnamed"), role)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PORT\tKIND\tPVID\tTAGGED\tUNTAGGED\tRUNNING")

	for _, p := range t.Ports {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%t\n",
			p.Name, p.Kind, orDash(p.PVID), vlanList(p.Tagged), vlanList(p.Untagged), p.Running)
	}

	_, _ = fmt.Fprintln(tw)
	_, _ = fmt.Fprintln(tw, "PORT\tMAC\tVLAN\tWIFI\tSSID\tBAND")

	for _, s := range t.Seen {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\t%s\n",
			s.Port, s.MAC, orDash(s.VLAN), s.WiFi, cmp.Or(s.SSID, "-"), cmp.Or(s.Band, "-"))
	}

	_, _ = fmt.Fprintln(tw)
	_, _ = fmt.Fprintln(tw, "PORT\tNEIGHBOUR\tMAC\tADDRESS\tPLATFORM\tBOARD\tTHEIR PORT")

	for _, n := range t.Neighbours {
		addr := "-"
		if n.Addr.IsValid() {
			addr = n.Addr.String()
		}

		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			n.Port, cmp.Or(n.Identity, "-"), cmp.Or(n.MAC, "-"), addr,
			cmp.Or(n.Platform, "-"), cmp.Or(n.Board, "-"), cmp.Or(n.TheirPort, "-"))
	}

	return tw.Flush()
}

// orDash renders a VLAN, showing none as a dash.
func orDash(n int) string {
	if n == 0 {
		return "-"
	}

	return strconv.Itoa(n)
}

// vlanList renders a set of VLANs as "10,20", or a dash when empty.
func vlanList(vlans []int) string {
	if len(vlans) == 0 {
		return "-"
	}

	parts := make([]string, len(vlans))
	for i, v := range vlans {
		parts[i] = strconv.Itoa(v)
	}

	return strings.Join(parts, ",")
}
