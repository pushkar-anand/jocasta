package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"text/tabwriter"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/db"
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
	conn *db.DB,
) error {
	rc, ok := cfg.Plugins.RouterOS[p.Name]
	if !ok {
		return fmt.Errorf("no source named %q is configured", p.Name)
	}

	src, err := newRouterOS(p.Name, rc, log)
	if err != nil {
		return err
	}

	facts, err := src.Discover(ctx)

	// A half-read source still has something to show, so the error is reported
	// alongside the rows rather than instead of them.
	if err != nil && len(facts) == 0 {
		return fmt.Errorf("discover %s: %w", src.Name(), err)
	}

	if err != nil {
		log.WarnContext(ctx, "source answered in part",
			slog.String("source", src.Name()),
			slog.Int("facts", len(facts)),
			logger.Err(err),
		)
	}

	if err := outputFacts(os.Stdout, facts, p.JSON); err != nil {
		return err
	}

	if !p.Save {
		return nil
	}

	return p.save(ctx, log, src, facts, conn)
}

// save records the reading. It runs after the facts are printed so a database
// that will not open still leaves the operator with the read they asked for.
func (p *PluginRunCmd) save(
	ctx context.Context,
	log *slog.Logger,
	src plugin.HostDiscoverer,
	facts []plugin.Fact,
	conn *db.DB,
) error {
	res, err := inventory.New(conn.Conn, log).RecordFacts(ctx, src.Name(), src.Kind(), facts)
	if err != nil {
		return fmt.Errorf("record facts: %w", err)
	}

	log.InfoContext(ctx, "recorded source",
		slog.String("source", src.Name()),
		slog.Int64("scan", res.ScanID),
		slog.Int("seen", res.Seen),
		slog.Int("discovered", res.Discovered),
		slog.Int("identified", res.Identified),
		slog.Int("merged", res.Merged),
		slog.Int("dropped", res.Dropped),
	)

	return nil
}

func outputFacts(w io.Writer, facts []plugin.Fact, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		return enc.Encode(facts)
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
