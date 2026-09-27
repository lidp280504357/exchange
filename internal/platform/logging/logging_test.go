package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
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

func TestNewRedactsSecretsAndMasksPII(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, FormatJSON, slog.LevelInfo).With("refresh_token", "rt-secret")
	log.Info("login",
		"password", "hunter2hunter2",
		"Authorization", "Bearer abc",
		"otp_ticket", "tk",
		"email", "alice@example.com",
		"phone", "+8613812341234",
		"identifier", "bob@example.com",
		"client_ip", "203.0.113.9",
		slog.Group("req", slog.String("captcha-token", "cf")),
		"code", "AUTH_OTP_INVALID",
	)
	out := buf.String()
	for _, leaked := range []string{"rt-secret", "hunter2", "Bearer abc", `"tk"`, "alice@", "13812341234", "bob@", "203.0.113.9", `"cf"`} {
		if strings.Contains(out, leaked) {
			t.Fatalf("leaked %q in %s", leaked, out)
		}
	}
	for _, kept := range []string{"a***@example.com", "+86138****1234", "b***@example.com", "203.0.113.*", "AUTH_OTP_INVALID", Redacted} {
		if !strings.Contains(out, kept) {
			t.Fatalf("missing %q in %s", kept, out)
		}
	}
}

func TestNewAddsContextAttributesAndTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, FormatJSON, slog.LevelInfo)

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))
	ctx = WithAttrs(ctx, slog.String("request_id", "req-1"))
	ctx = WithAttrs(ctx, slog.String("user_id", "u-1"), slog.String("email", "carol@example.com"))
	log.InfoContext(ctx, "hello")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"trace_id":   "4bf92f3577b34da6a3ce929d0e0e4736",
		"span_id":    "00f067aa0ba902b7",
		"request_id": "req-1",
		"user_id":    "u-1",
		"email":      "c***@example.com",
	}
	for k, v := range want {
		if rec[k] != v {
			t.Fatalf("%s = %v, want %v (record %v)", k, rec[k], v, rec)
		}
	}

	buf.Reset()
	log.Info("no context")
	if strings.Contains(buf.String(), "trace_id") {
		t.Fatalf("background records must not carry trace ids: %s", buf.String())
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
