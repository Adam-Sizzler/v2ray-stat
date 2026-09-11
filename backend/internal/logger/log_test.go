package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerFormatConsole(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	l, err := NewLoggerFromEnv("debug", FormatConsole, "UTC", &buf)
	if err != nil {
		t.Fatalf("NewLoggerFromEnv failed: %v", err)
	}
	l = l.RoleService("Workers", "TestService")
	l.Info("Hello world", "key1", "val1", "num", 42)

	out := buf.String()
	if !strings.Contains(out, "INFO [Workers] [TestService] Hello world key1=val1 num=42") {
		t.Fatalf("unexpected log output: %q", out)
	}
}

func TestLoggerBoxMessage(t *testing.T) {
	var buf bytes.Buffer
	l, err := NewLoggerFromEnv("info", FormatConsole, "UTC", &buf)
	if err != nil {
		t.Fatalf("NewLoggerFromEnv failed: %v", err)
	}
	l.Info("┌─────────────┐\n│ Box content │\n└─────────────┘")

	out := buf.String()
	if !strings.Contains(out, "│ Box content │") {
		t.Fatalf("unexpected box output: %q", out)
	}
	if strings.Contains(out, "INFO") {
		t.Fatalf("box message should be printed raw, got: %q", out)
	}
}
