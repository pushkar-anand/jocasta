// Command jocasta is the network inventory scanner and server.
package main

import (
	"github.com/alecthomas/kong"
	"github.com/pushkar-anand/jocasta/internal/config"
)

type CLI struct {
	ConfigFile string `name:"config" short:"c" default:"${configFile}" help:"Path to configuration file."`
	LogLevel   string `name:"log-level" help:"Override log level (debug, info, warn, error)."`
	LogFormat  string `name:"log-format" help:"Override log format (text, json)."`

	Serve ServeCmd `cmd:"" help:"Start the Jocasta web server." default:"withargs"`
	Scan  ScanCmd  `cmd:"" help:"Scan network addresses with ICMP echo requests."`

	Plugin PluginCmd `cmd:"" help:"Read a configured source directly."`

	Version VersionCmd `cmd:"" help:"Print build information."`
}

// newParser builds the parser for cli. Tests call it too, so the grammar they
// assert against is the one main actually runs.
func newParser(cli *CLI, opts ...kong.Option) (*kong.Kong, error) {
	return kong.New(cli, append([]kong.Option{
		kong.Name("jocasta"),
		kong.Description("Network discovery and homelab device inventory tool."),
		kong.Vars{"configFile": config.DefaultConfigFile},
	}, opts...)...)
}
