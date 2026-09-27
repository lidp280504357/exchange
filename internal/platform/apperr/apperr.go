// Package apperr defines the error model shared by every service: a stable
// machine-readable code (requirements appendix C), a message that is safe to
// show to clients, optional details, and a Kind that selects the HTTP and
// gRPC status. The wrapped cause is for logs only and never leaves the
// service.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind is the broad class of an error; HTTP status only reflects the class.
type Kind uint8

// Kinds, ordered from the most to the least common in handlers.
const (
	KindInternal        Kind = iota // bug or unexpected failure
	KindInvalid                     // malformed or out-of-range input
	KindUnauthenticated             // missing or bad credentials
	KindForbidden                   // authenticated but not allowed
	KindNotFound                    // resource does not exist
	KindConflict                    // state conflict, duplicates
	KindUnprocessable               // well-formed but violates a business rule
	KindRateLimited                 // quota exceeded
	KindUnavailable                 // dependency down, retry later
)

// HTTPStatus maps the kind to a status code.
func (k Kind) HTTPStatus() int {
	switch k {
	case KindInvalid:
		return http.StatusBadRequest
	case KindUnauthenticated:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict:
		return http.StatusConflict
	case KindUnprocessable:
		return http.StatusUnprocessableEntity
	case KindRateLimited:
		return http.StatusTooManyRequests
	case KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Common codes (prefix COMMON_). Service codes live with their services.
const (
	CodeInvalidArgument     = "COMMON_INVALID_ARGUMENT"
	CodeUnauthorized        = "COMMON_UNAUTHORIZED"
	CodeForbidden           = "COMMON_FORBIDDEN"
	CodeNotFound            = "COMMON_NOT_FOUND"
	CodeMethodNotAllowed    = "COMMON_METHOD_NOT_ALLOWED"
	CodeConflict            = "COMMON_CONFLICT"
	CodeIdempotencyConflict = "COMMON_IDEMPOTENCY_CONFLICT"
	CodeRateLimited         = "COMMON_RATE_LIMITED"
	CodeInternal            = "COMMON_INTERNAL"
	CodeUnavailable         = "COMMON_UNAVAILABLE"
)

// Error is an error with a client-facing code.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Details map[string]any
	cause   error
}

// New returns an error of the given kind and code.
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// Wrap attaches cause, which is logged but never sent to clients.
func Wrap(cause error, kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message, cause: cause}
}

// Error implements error. It includes the cause, so keep it out of
// responses; clients get Code and Message.
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return e.Code + ": " + e.Message
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.cause }

// WithDetail returns a copy of e with one more client-facing detail.
func (e *Error) WithDetail(key string, value any) *Error {
	c := *e
	c.Details = make(map[string]any, len(e.Details)+1)
	for k, v := range e.Details {
		c.Details[k] = v
	}
	c.Details[key] = value
	return &c
}

// From returns err as an *Error, turning unknown errors into an internal
// error that hides the cause.
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Wrap(err, KindInternal, CodeInternal, "internal error")
}

// Is reports whether err carries the given code.
func Is(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Shorthands for the common cases.

// Invalid reports bad input.
func Invalid(message string) *Error { return New(KindInvalid, CodeInvalidArgument, message) }

// NotFound reports a missing resource.
func NotFound(message string) *Error { return New(KindNotFound, CodeNotFound, message) }

// Unauthorized reports missing or bad credentials.
func Unauthorized(message string) *Error {
	return New(KindUnauthenticated, CodeUnauthorized, message)
}

// Forbidden reports a denied action.
func Forbidden(message string) *Error { return New(KindForbidden, CodeForbidden, message) }

// Internal wraps an unexpected failure.
func Internal(cause error) *Error {
	return Wrap(cause, KindInternal, CodeInternal, "internal error")
}

// Unavailable wraps a failed dependency.
func Unavailable(cause error) *Error {
	return Wrap(cause, KindUnavailable, CodeUnavailable, "service temporarily unavailable")
}
