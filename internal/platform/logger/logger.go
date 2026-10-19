// Package logger builds the application's structured logger.
//
// All code receives a *slog.Logger instead of using the global logger. Passing
// it explicitly makes dependencies visible and lets tests substitute a logger
// that discards output.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New creates a structured logger that writes to standard output.
//
// level accepts debug, info, warn, or error. format accepts "json" for
// production (machine-readable) or "text" for local development.
//
// Configuration is validated before this function is called, but unknown values
// still fall back to safe defaults so the logger is robust when reused.
func New(level, format string) *slog.Logger {
	options := &slog.HandlerOptions{
		Level: parseLevel(level),
	}

	var handler slog.Handler
	if strings.EqualFold(format, "json") {
		handler = slog.NewJSONHandler(os.Stdout, options)
	} else {
		handler = slog.NewTextHandler(os.Stdout, options)
	}

	// Common fields make every log line attributable when several services
	// write to the same aggregation system.
	return slog.New(handler).With("service", "taskflow")
}

// parseLevel converts a configuration string into a slog.Level.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
