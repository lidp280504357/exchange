// Package httpx holds the HTTP conventions shared by every service
// (requirements §7.1): JSON bodies, the unified error structure, request and
// trace IDs, client IP resolution behind proxies, access logs and metrics.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/tracing"
)

// MaxBodyBytes caps request bodies read by DecodeJSON.
const MaxBodyBytes = 1 << 20

// ErrorBody is the JSON structure of every error response.
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	TraceID string         `json:"trace_id"`
	Details map[string]any `json:"details,omitempty"`
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Warn("write json response", "error", err)
	}
}

// StatusClientClosedRequest is nginx's 499: the client went away before
// the answer.
const StatusClientClosedRequest = 499

// WriteError writes err in the unified error structure. Errors without a
// code become COMMON_INTERNAL, and their cause is logged instead of sent.
// When the client went away first (the request's context is canceled: a
// page closed, a logout cut short) nobody reads the answer and nothing
// failed here: the status is 499, logged at WARN, not a 5xx at ERROR
// (review B149).
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	e := apperr.From(err)
	if errors.Is(r.Context().Err(), context.Canceled) {
		slog.Default().WarnContext(r.Context(), "request canceled by the client", "status", StatusClientClosedRequest, "code", e.Code, "error", err)
		w.WriteHeader(StatusClientClosedRequest)
		return
	}
	status := e.Kind.HTTPStatus()
	if status >= http.StatusInternalServerError {
		slog.Default().ErrorContext(r.Context(), "request failed", "code", e.Code, "error", err)
	}
	WriteJSON(w, status, ErrorBody{
		Code:    e.Code,
		Message: e.Message,
		TraceID: tracing.TraceID(r.Context()),
		Details: e.Details,
	})
}

// DecodeJSON reads a single JSON object from the request body into dst,
// rejecting unknown fields, trailing data and bodies over MaxBodyBytes.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return invalidBody(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return apperr.Invalid("request body must contain a single JSON object")
	}
	return nil
}

func invalidBody(err error) error {
	var maxErr *http.MaxBytesError
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.As(err, &maxErr):
		return apperr.Invalid(fmt.Sprintf("request body exceeds %d bytes", maxErr.Limit))
	case errors.As(err, &typeErr):
		return apperr.Invalid("invalid value type").WithDetail("field", typeErr.Field)
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return apperr.Invalid("malformed JSON")
	case errors.Is(err, io.EOF):
		return apperr.Invalid("request body is empty")
	default:
		// json reports unknown fields as `json: unknown field "x"`.
		return apperr.Invalid(err.Error())
	}
}
