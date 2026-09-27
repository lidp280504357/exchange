package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewJSON(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, FormatJSON, slog.LevelInfo).Info("hello", "user_id", 42)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not JSON: %q: %v", buf.String(), err)
	}
	if rec["msg"] != "hello" || rec["level"] != "INFO" || rec["user_id"] != float64(42) {
		t.Fatalf("unexpected record: %v", rec)
	}
}

func TestNewText(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, FormatText, slog.LevelInfo).Info("hello", "user_id", 42)
	if out := buf.String(); !strings.Contains(out, "msg=hello") || !strings.Contains(out, "user_id=42") {
		t.Fatalf("unexpected text record: %q", out)
	}
}

func TestNewLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, FormatJSON, slog.LevelWarn)
	log.Info("dropped")
	log.Warn("kept")
	if out := buf.String(); strings.Contains(out, "dropped") || !strings.Contains(out, "kept") {
		t.Fatalf("level filter broken: %q", out)
	}
}

func TestFormatUnmarshalText(t *testing.T) {
	for _, s := range []string{"json", "text"} {
		var f Format
		if err := f.UnmarshalText([]byte(s)); err != nil || string(f) != s {
			t.Fatalf("%s: format = %q, err = %v", s, f, err)
		}
	}
	var f Format
	if err := f.UnmarshalText([]byte("logfmt")); err == nil {
		t.Fatal("unknown format must fail")
	}
}
