package grpcx

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/skill/exchange/internal/platform/apperr"
)

// errorDomain marks ErrorInfo details that carry an apperr code.
const errorDomain = "exchange"

var kindToCode = map[apperr.Kind]codes.Code{
	apperr.KindInternal:        codes.Internal,
	apperr.KindInvalid:         codes.InvalidArgument,
	apperr.KindUnauthenticated: codes.Unauthenticated,
	apperr.KindForbidden:       codes.PermissionDenied,
	apperr.KindNotFound:        codes.NotFound,
	apperr.KindConflict:        codes.AlreadyExists,
	apperr.KindUnprocessable:   codes.FailedPrecondition,
	apperr.KindRateLimited:     codes.ResourceExhausted,
	apperr.KindUnavailable:     codes.Unavailable,
}

var codeToKind = map[codes.Code]apperr.Kind{
	codes.InvalidArgument:    apperr.KindInvalid,
	codes.OutOfRange:         apperr.KindInvalid,
	codes.Unauthenticated:    apperr.KindUnauthenticated,
	codes.PermissionDenied:   apperr.KindForbidden,
	codes.NotFound:           apperr.KindNotFound,
	codes.AlreadyExists:      apperr.KindConflict,
	codes.Aborted:            apperr.KindConflict,
	codes.FailedPrecondition: apperr.KindUnprocessable,
	codes.ResourceExhausted:  apperr.KindRateLimited,
	codes.Unavailable:        apperr.KindUnavailable,
	codes.DeadlineExceeded:   apperr.KindUnavailable,
	codes.Canceled:           apperr.KindUnavailable,
}

// ToStatus converts a handler error into a gRPC status. Coded errors keep
// their code and their client-facing details in an ErrorInfo detail;
// anything else becomes an opaque Internal status, and the caller logs
// the cause.
func ToStatus(err error) *status.Status {
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok {
		return s
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.New(codes.Canceled, "canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.New(codes.DeadlineExceeded, "deadline exceeded")
	}
	e := apperr.From(err)
	code, ok := kindToCode[e.Kind]
	if !ok {
		code = codes.Internal
	}
	s := status.New(code, e.Message)
	info := &errdetails.ErrorInfo{Reason: e.Code, Domain: errorDomain, Metadata: encodeDetails(e.Details)}
	if withDetails, err := s.WithDetails(info); err == nil {
		s = withDetails
	}
	return s
}

// FromStatus converts an error returned by a client call back into an
// apperr error, so that a downstream service's code and details reach the
// HTTP response unchanged.
func FromStatus(err error) error {
	if err == nil {
		return nil
	}
	s, ok := status.FromError(err)
	if !ok {
		return err
	}
	for _, d := range s.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == errorDomain {
			kind, ok := codeToKind[s.Code()]
			if !ok {
				kind = apperr.KindInternal
			}
			e := apperr.Wrap(err, kind, info.GetReason(), s.Message())
			e.Details = decodeDetails(info.GetMetadata())
			return e
		}
	}
	if kind, ok := codeToKind[s.Code()]; ok && kind == apperr.KindUnavailable {
		return apperr.Unavailable(err)
	}
	return apperr.Internal(err)
}

// The most a detail may take as JSON, and all of them together: the
// details ride in the status trailer, whose size peers and proxies limit;
// past such a limit the call fails and the coded error arrives as an
// opaque internal one.
const (
	maxDetailBytes  = 1 << 10
	maxDetailsBytes = 4 << 10
)

// encodeDetails carries an error's details across the wire. ErrorInfo
// metadata holds strings, so each value goes as its JSON: a decimal as its
// quoted string, a count as a number. A value that does not encode, or
// is larger than maxDetailBytes, is left out, and so are those past
// maxDetailsBytes in all (taken in key order, so the same ones each time).
func encodeDetails(details map[string]any) map[string]string {
	if len(details) == 0 {
		return nil
	}
	out := make(map[string]string, len(details))
	total := 0
	for _, k := range slices.Sorted(maps.Keys(details)) {
		b, err := json.Marshal(details[k])
		if err != nil || len(b) > maxDetailBytes || total+len(k)+len(b) > maxDetailsBytes {
			continue
		}
		total += len(k) + len(b)
		out[k] = string(b)
	}
	return out
}

// decodeDetails restores what encodeDetails sent, numbers exact
// (json.Number), so the HTTP response shows them as the service that
// raised the error would have. A value that is not one whole JSON value (a
// peer sending plain strings such as 0xdeadbeef or "12 apples", which
// would otherwise decode to their first number) stays the string it is. A
// plain string that is JSON on its own (123, true, null) cannot be told
// apart and reads as that value.
func decodeDetails(meta map[string]string) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, raw := range meta {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil || strings.TrimSpace(raw[dec.InputOffset():]) != "" {
			v = raw
		}
		out[k] = v
	}
	return out
}
