// Package logger builds the application's structured logger.
//
// All code receives a *slog.Logger instead of using the global logger. Passing
// it explicitly makes dependencies visible and lets tests substitute a logger
// that discards output.
//
// Two output formats are supported:
//
//   - "text" (default) is meant for humans. It is rendered by
//     github.com/lmittmann/tint, which prints a compact line with a colored
//     level, a dimmed timestamp, dimmed attribute keys and highlighted
//     values. Colors are emitted only when standard output is a terminal, so
//     piping the output to a file or a container log collector stays clean.
//   - "json" is meant for machines. It is plain slog JSON and never contains
//     escape sequences.
package logger

import (
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"

	"github.com/lmittmann/tint"
)

// Color modes accepted by New.
const (
	// ColorAuto colorizes output when standard output is a terminal.
	ColorAuto = "auto"
	// ColorAlways colorizes output unconditionally.
	ColorAlways = "always"
	// ColorNever disables color, leaving plain text.
	ColorNever = "never"
)

// ANSI color codes used by the text handler. They are the standard SGR
// parameters: 30-37 are the normal foreground colors, 90-97 the bright ones.
//
// Only the error color is set here. The level badge is colored by tint
// itself, so the handler does not need its own level colors.
const (
	colorBrightRed uint8 = 9
)

// New creates a structured logger that writes to standard output.
//
// level accepts debug, info, warn, or error. format accepts "json" for
// production (machine-readable) or "text" for local development. color accepts
// "auto" (colorize when standard output is a terminal), "always", or "never".
//
// Configuration is validated before this function is called, but unknown values
// still fall back to safe defaults so the logger is robust when reused.
func New(level, format, color string) *slog.Logger {
	return NewWithWriter(os.Stdout, level, format, color)
}

// NewWithWriter is New with an explicit destination.
//
// The application uses New; tests use this variant to capture the output and
// assert on it.
func NewWithWriter(w io.Writer, level, format, color string) *slog.Logger {
	options := &slog.HandlerOptions{
		Level: parseLevel(level),
	}

	var handler slog.Handler
	if strings.EqualFold(format, "json") {
		handler = slog.NewJSONHandler(w, options)
	} else {
		handler = tint.NewTextHandler(w, tintOptions(w, level, color))
	}

	// Common fields make every log line attributable when several services
	// write to the same aggregation system.
	return slog.New(handler).With("service", "taskflow")
}

// tintOptions builds the options for the colorized text handler.
//
// The level is parsed again here because tint wants a slog.Leveler rather than
// the slog.HandlerOptions used by the JSON handler.
func tintOptions(w io.Writer, level, color string) *tint.Options {
	noColor := !useColor(w, color)
	options := &tint.Options{
		Level:   parseLevel(level),
		NoColor: noColor,
	}

	// The error highlighter is only installed for colored output: tint keeps
	// the color information inside the value, so it must not be attached when
	// the colors are stripped again.
	if !noColor {
		options.ReplaceAttr = highlight
	}
	return options
}

// useColor decides whether ANSI escape sequences should be written.
//
// "always" and "never" are explicit overrides. "auto" (the default) colorizes
// only when the destination is a terminal, so redirected output and container
// logs stay free of escape sequences.
func useColor(w io.Writer, color string) bool {
	switch strings.ToLower(strings.TrimSpace(color)) {
	case ColorAlways:
		return true
	case ColorNever:
		return false
	default:
		file, ok := w.(*os.File)
		return ok && isTerminal(file)
	}
}

// isTerminal reports whether the file is attached to a character device, which
// is the standard way of detecting a terminal without extra dependencies.
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// highlight paints error values so a log line can be scanned at a glance:
// the error turns red and every other attribute is passed through untouched.
//
// Previous version, kept for comparison. The function also recolored the
// level badge:
//
//	if len(groups) == 0 && attr.Key == slog.LevelKey {
//		if level, ok := attr.Value.Any().(slog.Level); ok {
//			return tint.Attr(levelColor(level), attr)
//		}
//	}
//
// That was re-implementing what tint already does. tint colors the level
// badge itself: INFO green, WARN yellow, ERROR red. The version below leaves
// the level to tint and only handles the error value.
func highlight(groups []string, attr slog.Attr) slog.Attr {
	if attr.Value.Kind() == slog.KindAny {
		if err, ok := attr.Value.Any().(error); ok && !isNilError(err) {
			return tint.Attr(colorBrightRed, slog.Any(attr.Key, err))
		}
	}
	return attr
}

// isNilError reports whether err carries no error at all.
//
// A nil pointer stored in an error interface is not equal to nil, so the
// plain `err != nil` check would treat a typed nil as a real error and paint
// it red. The reflection check finds the nil underneath the interface.
func isNilError(err error) bool {
	if err == nil {
		return true
	}
	value := reflect.ValueOf(err)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
}

// parseLevel converts a configuration string into a slog.Level.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
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
