package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestKindHTTPStatus(t *testing.T) {
	want := map[Kind]int{
		KindInternal:        http.StatusInternalServerError,
		KindInvalid:         http.StatusBadRequest,
		KindUnauthenticated: http.StatusUnauthorized,
		KindForbidden:       http.StatusForbidden,
		KindNotFound:        http.StatusNotFound,
		KindConflict:        http.StatusConflict,
		KindUnprocessable:   http.StatusUnprocessableEntity,
		KindRateLimited:     http.StatusTooManyRequests,
		KindUnavailable:     http.StatusServiceUnavailable,
	}
	for k, status := range want {
		if got := k.HTTPStatus(); got != status {
			t.Fatalf("kind %d: status %d, want %d", k, got, status)
		}
	}
}

func TestFromKeepsCodedErrorsThroughWrapping(t *testing.T) {
	base := New(KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient balance")
	wrapped := fmt.Errorf("transfer: %w", base)
	if got := From(wrapped); got != base {
		t.Fatalf("From lost the coded error: %v", got)
	}
	if !Is(wrapped, "LEDGER_INSUFFICIENT_BALANCE") || Is(wrapped, CodeInternal) {
		t.Fatal("Is does not match codes")
	}
}

func TestFromHidesUnknownErrors(t *testing.T) {
	cause := errors.New("pq: connection refused to 10.0.0.5")
	e := From(cause)
	if e.Kind != KindInternal || e.Code != CodeInternal || e.Message != "internal error" {
		t.Fatalf("unexpected mapping: %+v", e)
	}
	if !errors.Is(e, cause) {
		t.Fatal("cause must stay reachable for logs")
	}
}

func TestWithDetailCopies(t *testing.T) {
	base := Invalid("bad field")
	a := base.WithDetail("field", "email")
	b := a.WithDetail("reason", "format")
	if base.Details != nil || len(a.Details) != 1 || len(b.Details) != 2 {
		t.Fatalf("details must not alias: base=%v a=%v b=%v", base.Details, a.Details, b.Details)
	}
}

func TestErrorString(t *testing.T) {
	if got := Invalid("bad").Error(); got != "COMMON_INVALID_ARGUMENT: bad" {
		t.Fatalf("got %q", got)
	}
	if got := Internal(errors.New("boom")).Error(); got != "COMMON_INTERNAL: internal error: boom" {
		t.Fatalf("got %q", got)
	}
}
