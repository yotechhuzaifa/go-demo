package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestNewTextFormatIsCompact(t *testing.T) {
	var buffer bytes.Buffer
	log := NewWithWriter(&buffer, "info", "text", ColorNever)

	log.Info("step 1/9: configuration loaded", "env", "development")

	output := buffer.String()
	if strings.Contains(output, "\x1b[") {
		t.Errorf("output contains escape sequences: %q", output)
	}
	if !strings.Contains(output, "configuration loaded") {
		t.Errorf("output = %q, want it to contain the message", output)
	}
	if !strings.Contains(output, "env=development") {
		t.Errorf("output = %q, want it to contain the attribute", output)
	}
	if !strings.Contains(output, "service=taskflow") {
		t.Errorf("output = %q, want it to contain the service attribute", output)
	}
}

func TestNewTextFormatColorizesLevels(t *testing.T) {
	var buffer bytes.Buffer
	log := NewWithWriter(&buffer, "debug", "text", ColorAlways)

	log.Debug("debug line")
	log.Info("info line")
	log.Warn("warn line")
	log.Error("error line")

	output := buffer.String()
	if !strings.Contains(output, "\x1b[") {
		t.Errorf("output = %q, want escape sequences", output)
	}
	for _, badge := range []string{"DBG", "INF", "WRN", "ERR"} {
		if !strings.Contains(output, badge) {
			t.Errorf("output = %q, want it to contain the %q level badge", output, badge)
		}
	}
}

func TestNewTextFormatHighlightsErrors(t *testing.T) {
	var buffer bytes.Buffer
	log := NewWithWriter(&buffer, "info", "text", ColorAlways)

	log.Error("request failed", "err", errors.New("boom"))

	output := buffer.String()
	if !strings.Contains(output, "boom") {
		t.Errorf("output = %q, want it to contain the error message", output)
	}
	if !strings.Contains(output, "\x1b[") {
		t.Errorf("output = %q, want escape sequences", output)
	}
}

func TestNewNeverColorSkipsHighlighter(t *testing.T) {
	var buffer bytes.Buffer
	log := NewWithWriter(&buffer, "info", "text", ColorNever)

	log.Error("request failed", "err", errors.New("boom"))

	output := buffer.String()
	if strings.Contains(output, "\x1b[") {
		t.Errorf("output = %q, want no escape sequences", output)
	}
	if !strings.Contains(output, "boom") {
		t.Errorf("output = %q, want it to contain the error message", output)
	}
}

func TestNewJSONFormatStaysMachineReadable(t *testing.T) {
	var buffer bytes.Buffer
	log := NewWithWriter(&buffer, "info", "json", ColorAlways)

	log.Info("request completed", "status", 200)

	output := buffer.String()
	if strings.Contains(output, "\x1b[") {
		t.Errorf("output = %q, want no escape sequences", output)
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(output), &record); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, output)
	}
	if record["msg"] != "request completed" {
		t.Errorf("msg = %v, want %q", record["msg"], "request completed")
	}
	if record["status"] != float64(200) {
		t.Errorf("status = %v, want 200", record["status"])
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buffer bytes.Buffer
	log := NewWithWriter(&buffer, "warn", "text", ColorNever)

	log.Info("hidden")
	if buffer.Len() != 0 {
		t.Errorf("output = %q, want the info line to be filtered out", buffer.String())
	}

	log.Warn("visible")
	if !strings.Contains(buffer.String(), "visible") {
		t.Errorf("output = %q, want the warn line", buffer.String())
	}
}

func TestUseColor(t *testing.T) {
	var buffer bytes.Buffer
	tests := []struct {
		name  string
		color string
		want  bool
	}{
		{"always", ColorAlways, true},
		{"never", ColorNever, false},
		{"auto without terminal", ColorAuto, false},
		{"unknown value falls back to auto", "rainbow", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := useColor(&buffer, test.color); got != test.want {
				t.Errorf("useColor(%q) = %t, want %t", test.color, got, test.want)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		level string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}

	for _, test := range tests {
		if got := parseLevel(test.level); got != test.want {
			t.Errorf("parseLevel(%q) = %v, want %v", test.level, got, test.want)
		}
	}
}
