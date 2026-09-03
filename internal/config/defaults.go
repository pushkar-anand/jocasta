package config

import (
	"os"

	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

var defaults = map[string]any{
	"server.host": "localhost",
	"server.port": 8080,

	"db.path": ".",
	"db.name": "jocasta.db",

	"logger.level":  "info",
	"logger.format": "json",

	"inventory.online_window": inventory.DefaultOnlineWindow.String(),
	"inventory.address_grace": inventory.DefaultAddressGrace.String(),

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
