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
	Server struct {
		Host string `koanf:"host"`
		Port int    `koanf:"port"`
	}

	DB struct {
		Path string `koanf:"path"`
		Name string `koanf:"name"`
	}

	Logger struct {
		Level  string `koanf:"level"`
		Format string `koanf:"format"`
	}

	Inventory struct {
		// OnlineWindow is how long after a device was last seen it still counts
		// as online. Nothing announces a device leaving, so this belongs in
		// config: how stale a sighting may be depends on how often sweeps run.
		OnlineWindow time.Duration `koanf:"online_window"`
	}

	Config struct {
		Server    Server    `koanf:"server"`
		DB        DB        `koanf:"db"`
		Logger    Logger    `koanf:"logger"`
		Inventory Inventory `koanf:"inventory"`
		Networks  []string  `koanf:"networks"`
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

func (l Logger) FormatValue() logger.Format {
	if strings.EqualFold(l.Format, logger.FormatText.String()) {
		return logger.FormatText
	}

	return logger.FormatJSON
}
