// Package logging builds the structured logger every service uses.
package logging

import (
	"fmt"
	"io"
	"log/slog"
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
	opts := &slog.HandlerOptions{Level: level}
	if format == FormatText {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
