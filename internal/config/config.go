// Package config reads the runtime settings of jocasta from defaults, a YAML
// file, and the environment.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/pushkar-anand/build-with-go/config"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/notify"
)

// DefaultConfigFile is the path when --config is not given. A missing file at
// this path is not an error. Defaults and the environment can give all
// settings.
const DefaultConfigFile = "jocasta.yaml"

type (
	// Server tells where the HTTP server listens, and how it treats the
	// browsers and agents that reach it.
	Server struct {
		Host string `koanf:"host"`
		Port int    `koanf:"port"`
		CORS CORS   `koanf:"cors"`
		Auth Auth   `koanf:"auth"`
		MCP  MCP    `koanf:"mcp"`

		// TrustedProxies lists the reverse proxy addresses or CIDR prefixes
		// whose forwarding headers the server trusts to identify the client.
		// If it is empty, the server reads the client address from the
		// connection, and ignores forwarding headers.
		TrustedProxies []string `koanf:"trusted_proxies"`
	}

	// CORS tells which other origins can read the responses of the server
	// from a browser.
	CORS struct {
		// AllowedOrigins lists the origins (scheme://host[:port]) that can
		// make cross-origin requests. An example is a dashboard on a different
		// host that calls the JSON API from the browser. If it is empty, the
		// server sends no CORS headers. Then only the pages of the server can
		// read its responses.
		AllowedOrigins []string `koanf:"allowed_origins"`
	}

	// DB tells the location of the SQLite database.
	DB struct {
		Path string `koanf:"path"`
		Name string `koanf:"name"`
	}

	// Logger tells how to filter and render logs.
	Logger struct {
		Level  string `koanf:"level"`
		Format string `koanf:"format"`
	}

	// Inventory holds the settings that change how the inventory is read.
	Inventory struct {
		// OnlineWindow is how long a device counts as online after it was last
		// seen. No source announces that a device left. So this is a setting.
		// The age at which a sighting is too old depends on how often sweeps
		// run.
		OnlineWindow time.Duration `koanf:"online_window"`

		// AddressGrace is how long Jocasta keeps an address on which a swept
		// device stopped answering. After it, a sweep that finds the device on
		// a different address in the prefix retires the old address. Raise it
		// on a network with long DHCP leases, and hosts that hold an address
		// but do not use it.
		AddressGrace time.Duration `koanf:"address_grace"`
	}

	// Retention tells how long Jocasta keeps each kind of record. An hourly
	// prune deletes rows that are older than their window. Zero keeps that
	// kind forever.
	Retention struct {
		// History is how long Jocasta keeps the event log and the scan log.
		History time.Duration `koanf:"history"`

		// Traffic is how long Jocasta keeps hourly traffic totals.
		Traffic time.Duration `koanf:"traffic"`

		// DNS is how long Jocasta keeps hourly lookup counts.
		DNS time.Duration `koanf:"dns"`

		// Devices is how long Jocasta keeps a device after a scan last saw
		// it. It keeps a device that the owner labelled, grouped, typed,
		// noted, ignored or watched.
		Devices time.Duration `koanf:"devices"`
	}

	// Location tells where the network is.
	Location struct {
		// Country is the two-letter code of the country of the network. The
		// world map draws its lines from this country. If it is empty, the
		// outside address of the router sets it. A private outside address
		// cannot give the country. This is the case behind the carrier-grade
		// NAT of an ISP.
		Country string `koanf:"country"`

		// Timezone is the IANA name of the zone in which Jocasta shows times,
		// such as "Europe/Berlin". If it is empty, the TZ variable or the
		// system sets it. In a container, this is usually UTC.
		Timezone string `koanf:"timezone"`
	}

	// Scan holds the settings for how and when the poller sweeps the
	// network.
	Scan struct {
		// Source names the place from which these sweeps run. The inventory
		// records it as their origin. The default is the name of this host.
		// This is enough on a host with a fixed name. It is wrong in a
		// container, where the hostname is the container ID and changes on
		// every run.
		//
		// A new name does not rename the source. The inventory matches the
		// existing row by name. So a new name starts a new source, and the old
		// scan history stays under the old name.
		Source string `koanf:"source"`

		Devices struct {
			Enabled      bool          `koanf:"enabled"`
			Interval     time.Duration `koanf:"interval"`
			Rate         int           `koanf:"rate"`
			Rounds       int           `koanf:"rounds"`
			Wait         time.Duration `koanf:"wait"`
			ResolveNames bool          `koanf:"resolve_names"`
			ResolveMACs  bool          `koanf:"resolve_macs"`

			// ResolveMDNS makes the sweep ask for a name over mDNS. It asks
			// each host that answered and has no reverse DNS name. With
			// ResolveNames off, that is every host that answered.
			ResolveMDNS bool `koanf:"resolve_mdns"`

			// ResolveNetBIOS makes the sweep ask for a name over NetBIOS. It
			// asks each host that answered and has no name from reverse DNS or
			// mDNS.
			ResolveNetBIOS bool `koanf:"resolve_netbios"`

			// ResolveDNSSD makes the sweep browse the services that hosts
			// advertise over DNS-SD. A host that still has no name gets the
			// name that it gives its services.
			ResolveDNSSD bool `koanf:"resolve_dnssd"`

			// ResolveSSDP makes the sweep send one SSDP search. A host that
			// still has no name gets the friendlyName from its UPnP
			// description.
			ResolveSSDP bool `koanf:"resolve_ssdp"`
		} `koanf:"devices"`

		Ports struct {
			Enabled  bool          `koanf:"enabled"`
			Interval time.Duration `koanf:"interval"`

			// Custom is the ports that the scan probes, as a spec such as
			// "22,80,443,8000-8100". See scanner.ParsePortSpec. If it is
			// blank, the scan uses the preset.
			Custom string `koanf:"custom"`

			// Concurrency limits the connections that a scan opens at once.
			// The probe of every address is one burst of new flows. This
			// burst first fills the connection table of a cheap router. So
			// lower it on small equipment. Raise it only on real switches.
			Concurrency int `koanf:"concurrency"`
		} `koanf:"ports"`
	}

	// Source is what every router source has, for any transport. The config
	// of each kind embeds it. So a check for every kind reads it in the same
	// way.
	Source struct {
		Enabled bool          `koanf:"enabled"`
		Timeout time.Duration `koanf:"timeout"`

		// TopologyOnly marks a switch or access point that Jocasta reads only
		// for what is connected to it. The router gives its devices and
		// segments. The management address of a switch would rename a
		// segment. Its almost empty ARP table would look like devices that
		// left.
		TopologyOnly bool `koanf:"topology_only"`
	}

	// HTTPLogin tells where a source over HTTP is, and how to sign in to it.
	// A kind with a different transport does not embed it.
	//
	// The credentials stay here and never reach the store. The store sees
	// only the instance name and the facts.
	HTTPLogin struct {
		// Host is the router's address or name, without a port.
		Host string `koanf:"host"`
		Port int    `koanf:"port"`

		User     string `koanf:"user"`
		Password string `koanf:"password"`

		// SSL selects https. Insecure skips certificate verification. The
		// usual setup needs it, because routers serve a self-signed
		// certificate until you install one.
		SSL      bool `koanf:"ssl"`
		Insecure bool `koanf:"insecure"`
	}

	// RouterOS names one MikroTik router to read devices from. Jocasta reads
	// it over the REST API of the www or www-ssl service.
	RouterOS struct {
		Source    `koanf:",squash"`
		HTTPLogin `koanf:",squash"`
	}

	// OpenWrt names one OpenWrt router to read devices from. Jocasta reads it
	// over the ubus JSON-RPC endpoint that uhttpd serves at /ubus. User and
	// Password are an rpcd login, from /etc/config/rpcd.
	OpenWrt struct {
		Source    `koanf:",squash"`
		HTTPLogin `koanf:",squash"`
	}

	// NetFlow names one UDP listener for the flows that a router exports over
	// NetFlow v5, v9 or IPFIX.
	NetFlow struct {
		Enabled bool `koanf:"enabled"`

		// Listen is the UDP address to bind, such as ":2055". If it is empty,
		// the listener uses DefaultNetFlowListen.
		Listen string `koanf:"listen"`

		// Exporters are the addresses of the routers that can send. UDP has
		// no authentication. So the listener discards a datagram from any
		// other address without a read. An empty list is a config error.
		Exporters []string `koanf:"exporters"`
	}

	// Technitium names one Technitium DNS Server whose query log Jocasta
	// reads. Jocasta reads it over the HTTP API of its web service.
	//
	// For a cluster, add one entry for each node, at the address of that
	// node. Each node logs the lookups that it answered to its own disk. A
	// virtual address that the nodes share reaches only one node at a time.
	Technitium struct {
		Enabled bool          `koanf:"enabled"`
		Timeout time.Duration `koanf:"timeout"`

		// Host is the server's address or name, without a port.
		Host string `koanf:"host"`
		Port int    `koanf:"port"`

		// Token is an API token. A user with only Logs: View is enough.
		Token string `koanf:"token"`

		// SSL selects https. Insecure skips certificate verification, for a
		// server with a self-signed certificate.
		SSL      bool `koanf:"ssl"`
		Insecure bool `koanf:"insecure"`

		// LogApp names the installed app whose log Jocasta reads, as the Apps
		// page shows it. If it is empty, Jocasta reads the only app that logs
		// queries.
		LogApp string `koanf:"log_app"`
	}

	// PiHole names one Pi-hole v6 whose query log Jocasta reads. Jocasta
	// reads it over the API of its web server.
	PiHole struct {
		Enabled bool          `koanf:"enabled"`
		Timeout time.Duration `koanf:"timeout"`

		// Host is the server's address or name, without a port.
		Host string `koanf:"host"`
		Port int    `koanf:"port"`

		// Password is an app password, from the web interface of Pi-hole. It
		// can do everything that the web interface can, because Pi-hole has
		// no read-only login. Leave it empty for a Pi-hole with no
		// password.
		Password string `koanf:"password"`

		// SSL selects https. Insecure skips certificate verification. Use it
		// for the self-signed certificate that Pi-hole serves by default.
		SSL      bool `koanf:"ssl"`
		Insecure bool `koanf:"insecure"`
	}

	// AdGuard names one AdGuard Home whose query log Jocasta reads. Jocasta
	// reads it over the API of its web interface.
	AdGuard struct {
		Enabled bool          `koanf:"enabled"`
		Timeout time.Duration `koanf:"timeout"`

		// Host is the server's address or name, without a port.
		Host string `koanf:"host"`
		Port int    `koanf:"port"`

		// User and Password are the web interface login. It can do everything
		// that the web interface can, because AdGuard Home has no read-only
		// users. Leave User empty for a server with no login.
		User     string `koanf:"user"`
		Password string `koanf:"password"`

		// SSL selects https. Insecure skips certificate verification, for a
		// self-signed certificate.
		SSL      bool `koanf:"ssl"`
		Insecure bool `koanf:"insecure"`
	}

	// Auth sets how long a browser stays signed in, and whether its cookie
	// works only over HTTPS. jocasta sets the name, path and other flags of
	// the cookie itself.
	Auth struct {
		// SessionLifetime is the longest time that a session lasts after
		// sign-in, with or without activity.
		SessionLifetime time.Duration `koanf:"session_lifetime"`

		// IdleTimeout is how long a session stays without a request. Then the
		// server ends it.
		IdleTimeout time.Duration `koanf:"idle_timeout"`

		// CookieSecure limits the session cookie to HTTPS. It is on by
		// default. Set it to false only to sign in over plain HTTP on a
		// trusted network during development.
		CookieSecure bool `koanf:"cookie_secure"`
	}

	// MCP controls the Model Context Protocol endpoint for AI agents.
	MCP struct {
		// Enabled serves /mcp. It is off by default. The endpoint always needs
		// an API token. Also, the owner must choose to give the inventory to an
		// agent.
		Enabled bool `koanf:"enabled"`
	}

	// Plugins holds the sources other than the sweep. The key of each block
	// is an instance name. This name becomes the source of its facts.
	//
	// Each block is a map. So an environment override can name one instance.
	// A list would decode an override into one zero-valued element, with no
	// error. Then every configured instance would be lost, with no warning.
	Plugins struct {
		RouterOS map[string]RouterOS `koanf:"routeros"`
		OpenWrt  map[string]OpenWrt  `koanf:"openwrt"`
		NetFlow  map[string]NetFlow  `koanf:"netflow"`

		Technitium map[string]Technitium `koanf:"technitium"`
		PiHole     map[string]PiHole     `koanf:"pihole"`
		AdGuard    map[string]AdGuard    `koanf:"adguard"`
	}

	// Config is the whole set of named, nested settings.
	Config struct {
		Server    Server    `koanf:"server"`
		DB        DB        `koanf:"db"`
		Logger    Logger    `koanf:"logger"`
		Inventory Inventory `koanf:"inventory"`
		Retention Retention `koanf:"retention"`
		Location  Location  `koanf:"location"`
		Networks  []string  `koanf:"networks"`
		Scan      Scan      `koanf:"scan"`
		Plugins   Plugins   `koanf:"plugins"`

		// Notify is where Jocasta sends changes. It has one entry for each
		// destination, with its name as key. Jocasta reads its credentials
		// only from here, and never stores them in the database.
		//
		// It is a map. So an environment variable can override one
		// destination by name.
		Notify map[string]notify.Config `koanf:"notify"`
	}
)

// New builds the configuration from defaults, the YAML file at cfgFile, and the
// environment.
//
// A missing file at DefaultConfigFile is not a problem. But New reports an
// explicit path that does not exist. Defaults with no warning could bind the
// server to the wrong address, or point it at the wrong database.
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
		config.WithSplitKeyFor(",",
			"server.trusted_proxies",
			"server.cors.allowed_origins",
			"networks",
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	return cfg, nil
}

// SlogLevel maps the configured level name to an slog.Level. For an unknown
// name, it returns info.
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

// FormatValue maps the configured format to a logger.Format. For any value
// other than the text form, it returns JSON.
func (l Logger) FormatValue() logger.Format {
	if strings.EqualFold(l.Format, logger.FormatText.String()) {
		return logger.FormatText
	}

	return logger.FormatJSON
}

// Common returns the settings that every router source has. So code that
// treats all kinds the same can read them from the config of any kind.
func (s Source) Common() Source {
	return s
}

// PlainHTTP reports whether Jocasta reaches the source over http. Over http,
// the password goes over the network as plain text.
func (h HTTPLogin) PlainHTTP() bool {
	return !h.SSL
}
