package config

import (
	"os"

	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

var defaults = map[string]any{
	"server.host": "localhost",
	"server.port": 8080,

	"server.auth.session_lifetime": "168h",
	"server.auth.idle_timeout":     "24h",
	"server.auth.cookie_secure":    true,

	"server.mcp.enabled": false,

	"db.path": ".",
	"db.name": "jocasta.db",

	"logger.level":  "info",
	"logger.format": "json",

	"inventory.online_window": inventory.DefaultOnlineWindow.String(),
	"inventory.address_grace": inventory.DefaultAddressGrace.String(),

	"retention.history": inventory.DefaultRetention.String(),
	"retention.traffic": inventory.DefaultTrafficRetention.String(),
	"retention.dns":     inventory.DefaultDNSRetention.String(),
	"retention.devices": inventory.DefaultDeviceRetention.String(),

	"location.country":  "",
	"location.timezone": "",

	"scan.source": defaultSource(),

	"scan.devices.enabled":         true,
	"scan.devices.interval":        "5m",
	"scan.devices.rate":            1000,
	"scan.devices.rounds":          2,
	"scan.devices.wait":            "2s",
	"scan.devices.resolve_names":   true,
	"scan.devices.resolve_macs":    true,
	"scan.devices.resolve_mdns":    true,
	"scan.devices.resolve_netbios": true,
	"scan.devices.resolve_dnssd":   true,
	"scan.devices.resolve_ssdp":    true,
	"scan.ports.enabled":           false,
	"scan.ports.interval":          "6h",
	"scan.ports.custom":            "",
	"scan.ports.concurrency":       scanner.DefaultConcurrency,
}

// DefaultNetFlowListen is the UDP address that a NetFlow instance binds when it
// names none. 2055 is the default port of exporters. It is a constant, because
// a map-keyed block has no static key path in the defaults map for a
// default.
const DefaultNetFlowListen = ":2055"

// defaultSource names the place from which sweeps run, when no setting gives
// one. A host with a fixed name identifies itself well enough. A container
// does not, because its hostname is the container ID and changes on every
// run. scan.source exists to override this.
func defaultSource() string {
	host, err := os.Hostname()
	if err != nil {
		return "sweep"
	}

	return "sweep:" + host
}
