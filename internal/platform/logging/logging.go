// Package logging builds the structured logger every service uses.
//
// Records are scrubbed by attribute key before they are written: secrets
// (password, token, otp, authorization ...) become "[REDACTED]" and personal
// data (email, phone, identifier, ip ...) is masked with package pii. Use the
// *Context logging methods on request paths: the logger then adds trace_id,
// span_id and any attributes attached to the context with WithAttrs.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/lidp280504357/exchange/internal/platform/pii"
)

// Format selects the log encoding, read from LOG_FORMAT.
type Format string

// Supported formats.
const (
	FormatJSON Format = "json" // one JSON object per line, for servers
	FormatText Format = "text" // key=value lines, for local development
)

// UnmarshalText accepts "json" or "text".
func (f *Format) UnmarshalText(b []byte) error {
	switch v := Format(b); v {
	case FormatJSON, FormatText:
		*f = v
		return nil
	default:
		return fmt.Errorf("unknown log format %q, want json or text", v)
	}
}

// New returns a logger that writes records at or above level to w. Any
// format other than FormatText produces JSON.
func New(w io.Writer, format Format, level slog.Leveler) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: redact}
	var h slog.Handler
	if format == FormatText {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(contextHandler{h})
}

// Redacted replaces secrets in logs.
const Redacted = "[REDACTED]"

var secretKeys = map[string]bool{
	"password": true, "old_password": true, "new_password": true, "passwd": true,
	"secret": true, "api_key": true, "private_key": true, "signing_key": true,
	"token": true, "access_token": true, "refresh_token": true, "step_up_token": true,
	"otp": true, "otp_code": true, "otp_ticket": true, "captcha_token": true,
	"authorization": true, "cookie": true, "set_cookie": true,
}

var maskers = map[string]func(string) string{
	"email":       pii.MaskEmail,
	"phone":       pii.MaskPhone,
	"identifier":  pii.MaskIdentifier,
	"target":      pii.MaskIdentifier,
	"ip":          pii.MaskIP,
	"client_ip":   pii.MaskIP,
	"remote_ip":   pii.MaskIP,
	"remote_addr": pii.MaskIP,
}

// redact is the slog ReplaceAttr hook; the value is already resolved.
func redact(_ []string, a slog.Attr) slog.Attr {
	key := strings.ReplaceAll(strings.ToLower(a.Key), "-", "_")
	if secretKeys[key] {
		return slog.String(a.Key, Redacted)
	}
	if mask, ok := maskers[key]; ok && a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, mask(a.Value.String()))
	}
	return a
}

type ctxAttrsKey struct{}

// WithAttrs returns a context whose log records carry attrs in addition to
// those already attached.
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(ctxAttrsKey{}).([]slog.Attr)
	merged := make([]slog.Attr, 0, len(prev)+len(attrs))
	merged = append(merged, prev...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, ctxAttrsKey{}, merged)
}

// contextHandler adds trace identifiers and context attributes to records.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	if attrs, ok := ctx.Value(ctxAttrsKey{}).([]slog.Attr); ok {
		r.AddAttrs(attrs...)
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
