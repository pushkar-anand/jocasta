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
}
