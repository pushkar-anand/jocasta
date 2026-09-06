// Package config reads jocasta's runtime settings from defaults, a YAML file,
// and the environment.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/pushkar-anand/build-with-go/config"
	"github.com/pushkar-anand/build-with-go/logger"
)

// DefaultConfigFile is the path used when --config is not given. A missing file
// here is not an error: defaults and the environment can supply everything.
const DefaultConfigFile = "jocasta.yaml"

type (
	// Server says where the HTTP server listens.
	Server struct {
		Host string `koanf:"host"`
		Port int    `koanf:"port"`
		CORS CORS   `koanf:"cors"`
	}

	// CORS says which origins outside the server's own may read its responses
	// from a browser.
	CORS struct {
		// AllowedOrigins lists the origins (scheme://host[:port]) permitted to
		// make cross-origin requests, such as a dashboard hosted elsewhere that
		// calls the JSON API from the browser. Blank leaves it to the server to
		// default to its own address, which is what a browser already allows
		// without CORS -- so an unset list changes nothing until an origin
		// outside it is added here.
		AllowedOrigins []string `koanf:"allowed_origins"`
	}

	// MCP controls whether the agent endpoint is available.
	MCP struct {
		Enabled bool `koanf:"enabled"`
	}

	// DB says where the SQLite database lives.
	DB struct {
		Path string `koanf:"path"`
		Name string `koanf:"name"`
	}

	// Logger says how logs are filtered and rendered.
	Logger struct {
		Level  string `koanf:"level"`
		Format string `koanf:"format"`
	}

	// Inventory holds the knobs that change how the inventory is read.
	Inventory struct {
		// OnlineWindow is how long after a device was last seen it still counts
		// as online. Nothing announces a device leaving, so this belongs in
		// config: how stale a sighting may be depends on how often sweeps run.
		OnlineWindow time.Duration `koanf:"online_window"`

		// AddressGrace is how long an address a swept device stopped answering
		// on is kept before a later sweep that finds the device elsewhere in
		// the prefix retires it. Raise it on a network with long DHCP leases
		// and hosts that hold an address without using it.
		AddressGrace time.Duration `koanf:"address_grace"`
	}

	// Scan holds settings for how and when the poller sweeps the network.
	Scan struct {
		// Source names the vantage point these sweeps are taken from, which is
		// what the inventory records as their provenance. It defaults to this
		// host's name, which is enough on a host that keeps one and wrong in a
		// container, where the hostname is the container ID and changes on every
		// run.
		//
		// Renaming does not rename the source: the existing row is matched by
		// name, so a new name starts a new one and leaves the old scan history
		// filed under the old.
		Source string `koanf:"source"`

		Devices struct {
			Enabled      bool          `koanf:"enabled"`
			Interval     time.Duration `koanf:"interval"`
			Rate         int           `koanf:"rate"`
			Rounds       int           `koanf:"rounds"`
			Wait         time.Duration `koanf:"wait"`
			ResolveNames bool          `koanf:"resolve_names"`
			ResolveMACs  bool          `koanf:"resolve_macs"`
		} `koanf:"devices"`

		Ports struct {
			Enabled  bool          `koanf:"enabled"`
			Interval time.Duration `koanf:"interval"`

			// Custom is the ports the scan probes, as a spec like
			// "22,80,443,8000-8100" (see scanner.ParsePortSpec). Blank leaves
			// the curated preset in place.
			Custom string `koanf:"custom"`

			// Concurrency caps the connections a scan opens at once. The whole
			// probe of every address is one burst of new flows, and a cheap
			// router's connection table is what that strains first, so lower it
			// on modest gear and raise it only on real switching.
			Concurrency int `koanf:"concurrency"`
		} `koanf:"ports"`
	}

	// RouterOS names one MikroTik router to read devices from.
	//
	// The credentials stay here and never reach the store, which only ever sees
	// the instance name and the facts.
	RouterOS struct {
		Enabled bool `koanf:"enabled"`

		// Host is the router's address or name, without a port.
		Host string `koanf:"host"`
		Port int    `koanf:"port"`

		User     string `koanf:"user"`
		Password string `koanf:"password"`

		// SSL selects https. Insecure skips certificate verification, which the
		// common setup needs: RouterOS serves a self-signed certificate for
		// www-ssl unless one is imported.
		SSL      bool `koanf:"ssl"`
		Insecure bool `koanf:"insecure"`

		Timeout time.Duration `koanf:"timeout"`
	}

	// Plugins holds the sources beyond the sweep, each block keyed by an
	// instance name that becomes the source these facts are filed under.
	//
	// A map rather than a list, so an environment override addresses one
	// instance by name. A list decodes an override into a single zero-valued
	// element and reports no error, which loses every configured instance
	// silently.
	Plugins struct {
		RouterOS map[string]RouterOS `koanf:"routeros"`
	}

	// Config is the whole set of named, nested settings.
	Config struct {
		Server    Server    `koanf:"server"`
		MCP       MCP       `koanf:"mcp"`
		DB        DB        `koanf:"db"`
		Logger    Logger    `koanf:"logger"`
		Inventory Inventory `koanf:"inventory"`
		Networks  []string  `koanf:"networks"`
		Scan      Scan      `koanf:"scan"`
		Plugins   Plugins   `koanf:"plugins"`
	}
)

// New assembles configuration from defaults, the YAML file at cfgFile, and the
// environment.
//
// A missing file at DefaultConfigFile is fine, but an explicit path that does
// not exist is reported: silently falling back to defaults would bind the
// server to the wrong address or point it at the wrong database.
func New(
	cfgFile string,
) (*Config, error) {
	if cfgFile != DefaultConfigFile {
		if _, err := os.Stat(cfgFile); err != nil {
			return nil, fmt.Errorf("config file %q: %w", cfgFile, err)
		}
	}

	cfg, err := config.Load[Config](
		config.WithDefaults(defaults),
		config.WithYAML(cfgFile),
		config.WithEnvPrefix("JOCASTA_"),
	)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	return cfg, nil
}

// SlogLevel maps the configured level name to an slog.Level, defaulting to
// info when the name is not recognised.
func (l Logger) SlogLevel() slog.Level {
	switch strings.ToLower(l.Level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warning", "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}

	return slog.LevelInfo
}

// FormatValue maps the configured format to a logger.Format, defaulting to
// JSON for anything other than the text form.
func (l Logger) FormatValue() logger.Format {
	if strings.EqualFold(l.Format, logger.FormatText.String()) {
		return logger.FormatText
	}

	return logger.FormatJSON
}
