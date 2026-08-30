package main

import (
	"log/slog"
	"strings"

	"github.com/pushkar-anand/build-with-go/logger"
)

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

	Config struct {
		Server Server `koanf:"server"`
		DB     DB     `koanf:"db"`
		Logger Logger `koanf:"logger"`
	}
)

var defaults = map[string]any{
	"server.host": "localhost",
	"server.port": 8080,

	"db.path": ".",
	"db.name": "jocasta.db",

	"logger.level":  "info",
	"logger.format": "json",
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
