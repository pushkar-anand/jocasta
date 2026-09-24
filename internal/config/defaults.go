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

	"location.country":  "",
	"location.timezone": "",

	"scan.source": defaultSource(),

	"scan.devices.enabled":       true,
	"scan.devices.interval":      "5m",
	"scan.devices.rate":          1000,
	"scan.devices.rounds":        2,
	"scan.devices.wait":          "2s",
	"scan.devices.resolve_names": true,
	"scan.devices.resolve_macs":  true,
	"scan.ports.enabled":         false,
	"scan.ports.interval":        "6h",
	"scan.ports.custom":          "",
	"scan.ports.concurrency":     scanner.DefaultConcurrency,
}

// DefaultNetFlowListen is the UDP address a NetFlow instance binds when it
// names none: 2055 is the port exporters default to. It lives here rather than
// in the defaults map because a map-keyed block has no static key path to put
// a default on.
const DefaultNetFlowListen = ":2055"

// defaultSource names the vantage point sweeps are taken from when nothing
// configures one. A host that keeps its name identifies itself well enough; a
// container does not, since its hostname is the container ID and changes on
// every run, which is what scan.source is there to override.
func defaultSource() string {
	host, err := os.Hostname()
	if err != nil {
		return "sweep"
	}

	return "sweep:" + host
}
