package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/poller"
)

type PluginCmd struct {
	Run PluginRunCmd `cmd:"" help:"Read and print the result from the configured source."`
}

type PluginRunCmd struct {
	Name string `arg:"" help:"Instance name of the source to read, as it appears under plugins."`
	JSON bool   `name:"json" help:"Output facts as JSON."`
	Save bool   `name:"save" help:"Record the facts in the device inventory."`
}

// Run reads one source without starting the server. Use it to check a
// credential or a firewall rule against the real device.
//
// It reads the source even when config disables it. An operator who names an
// instance is asking about that instance. Making them enable it first would
// mean editing config to test the config.
func (p *PluginRunCmd) Run(
	ctx context.Context,
	cfg *config.Config,
	log *slog.Logger,
	store *inventory.Store,
) error {
	resolvers, err := resolverInstances(cfg)
	if err != nil {
		return err
	}

	if in, ok := resolvers[p.Name]; ok {
		return p.runQueries(ctx, cfg, log, store, in)
	}

	built, err := newRouterSource(cfg, p.Name, log)
	if err != nil {
		return err
	}

	if t, ok := built.(plugin.TopologyScoped); ok && t.IsTopologyOnly() {
		reader, ok := built.(plugin.TopologyReader)
		if !ok {
			return fmt.Errorf("source %q is topology_only but cannot read topology", p.Name)
		}

		return p.runTopology(ctx, log, reader, store)
	}

	src, ok := built.(routerReader)
	if !ok {
		return fmt.Errorf("source %q cannot list devices", p.Name)
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
	if err != nil {
		return fmt.Errorf("discover %s: %w", src.Name(), err)
	}

	if err := outputNetworks(os.Stdout, nets, p.JSON); err != nil {
		return err
	}

	if err := outputFacts(os.Stdout, facts, p.JSON); err != nil {
		return err
	}

	var topo *plugin.Topology

	reader, readsTopology := built.(plugin.TopologyReader)
	if readsTopology {
		if topo, err = p.printTopology(ctx, log, reader); err != nil {
			return err
		}
	}

	if !p.Save {
		return nil
	}

	if err := p.save(ctx, log, src, nets, facts, store); err != nil {
		return err
	}

	if topo == nil {
		return nil
	}

	return poller.SaveTopology(ctx, store, log, reader, *topo)
}

// runQueries reads one batch of a resolver's query log, the amount one poll
// takes, and prints who looked up what. It starts an hour back. With --save it
// starts where the last saved read stopped and saves the batch. That way
// nothing is counted twice.
func (p *PluginRunCmd) runQueries(
	ctx context.Context,
	cfg *config.Config,
	log *slog.Logger,
	store *inventory.Store,
	in resolverInstance,
) error {
	// The names of every kind are checked against each other, as the web server
	// does at boot.
	if _, err := routerInstances(cfg); err != nil {
		return err
	}

	src, err := in.build(log)
	if err != nil {
		return err
	}

	var cursor string

	if p.Save {
		if cursor, err = store.QueryCursor(ctx, src.Name()); err != nil {
			return err
		}
	}

	queries, next, err := src.Queries(ctx, cursor)
	if err != nil {
		return fmt.Errorf("read query log from %s: %w", src.Name(), err)
	}

	if err := outputQueries(os.Stdout, queries, p.JSON); err != nil {
		return err
	}

	if !p.Save {
		return nil
	}

	res, err := store.RecordQueries(ctx, src.Name(), src.Kind(), queries, next)
	if err != nil {
		return fmt.Errorf("record lookups: %w", err)
	}

	log.InfoContext(ctx, "recorded lookups",
		slog.String("src", src.Name()),
		slog.Int("recorded", res.Recorded),
		slog.Int("dropped", res.Dropped),
	)

	return nil
}

// routerReader is a source that can both list the devices and network segments
// it serves. All routers have this capability.
type routerReader interface {
	plugin.HostDiscoverer
	plugin.NetworkDiscoverer
}

// runTopology reads which devices are connected to a switch or access point,
// for the topology map.
func (p *PluginRunCmd) runTopology(
	ctx context.Context,
	log *slog.Logger,
	src plugin.TopologyReader,
	store *inventory.Store,
) error {
	topo, err := src.Topology(ctx)
	if err != nil {
		return fmt.Errorf("read topology from %s: %w", src.Name(), err)
	}

	if err := outputTopology(os.Stdout, topo, p.JSON); err != nil {
		return err
	}

	if !p.Save {
		return nil
	}

	return poller.SaveTopology(ctx, store, log, src, topo)
}

// printTopology reads which devices are connected to a router and prints them.
// It returns the topology for the save. A failed read is only logged, and the
// result is nil with no error: the devices already read matter more.
func (p *PluginRunCmd) printTopology(
	ctx context.Context,
	log *slog.Logger,
	src plugin.TopologyReader,
) (*plugin.Topology, error) {
	topo, err := src.Topology(ctx)
	if err != nil {
		log.WarnContext(ctx, "source did not say what is plugged into it",
			slog.String("src", src.Name()),
			logger.Err(err),
		)

		return nil, nil
	}

	if err := outputTopology(os.Stdout, topo, p.JSON); err != nil {
		return nil, err
	}

	return &topo, nil
}

// save records the discovered results in the database. It runs after they are
// printed, so the operator still sees them when the database will not open.
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
		slog.Int("back", res.Back),
		slog.Int("quiet", res.Quiet),
	)

	return nil
}

// outputNetworks prints the segments the source described. Under --json,
// networks and facts are separate documents. That way jq gets each one with no
// wrapper.
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

// outputFacts writes results as JSON or a table. With no results, it prints a
// message instead of the table.
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

// outputQueries prints the lookups a resolver logged. Under --json it writes
// each lookup. Otherwise it prints one row per client and name, with how often
// it was looked up, blocked and failed. Each client's busiest names come first.
func outputQueries(w io.Writer, queries []plugin.Query, asJSON bool) error {
	if asJSON {
		return writeJSON(w, queries)
	}

	if len(queries) == 0 {
		_, err := fmt.Fprintln(w, "Source logged no lookups. They appear once devices send their lookups to it.")

		return err
	}

	type key struct {
		client netip.Addr
		name   string
	}

	type counts struct{ lookups, blocked, failed int }

	rows := make(map[key]*counts)

	for _, q := range queries {
		k := key{client: q.Client, name: q.Name}

		c, ok := rows[k]
		if !ok {
			c = &counts{}
			rows[k] = c
		}

		c.lookups++

		if q.Blocked {
			c.blocked++
		}

		if q.Failed {
			c.failed++
		}
	}

	keys := slices.SortedFunc(maps.Keys(rows), func(a, b key) int {
		return cmp.Or(
			a.client.Compare(b.client),
			cmp.Compare(rows[b].lookups, rows[a].lookups),
			strings.Compare(a.name, b.name),
		)
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CLIENT\tNAME\tLOOKUPS\tBLOCKED\tFAILED")

	for _, k := range keys {
		c := rows[k]
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\n", k.client, k.name, c.lookups, c.blocked, c.failed)
	}

	return tw.Flush()
}

// outputTopology prints what is connected to a source: its ports, the addresses
// learned on each, and its neighbours.
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

// orDash renders a VLAN, or a dash for none.
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
