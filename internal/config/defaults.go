package config

import "github.com/pushkar-anand/jocasta/internal/inventory"

var defaults = map[string]any{
	"server.host": "localhost",
	"server.port": 8080,

	"db.path": ".",
	"db.name": "jocasta.db",

	"logger.level":  "info",
	"logger.format": "json",

	"inventory.online_window": inventory.DefaultOnlineWindow.String(),

	"scan.devices.enabled":       true,
	"scan.devices.interval":      "5m",
	"scan.devices.rate":          1000,
	"scan.devices.rounds":        2,
	"scan.devices.wait":          "2s",
	"scan.devices.resolve_names": true,
	"scan.devices.resolve_macs":  true,
	"scan.ports.enabled":         false,
	"scan.ports.interval":        "6h",
}
