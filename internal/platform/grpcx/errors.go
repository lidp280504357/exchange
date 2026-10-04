package grpcx

import (
	"context"
	"errors"

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
// their code in an ErrorInfo detail; anything else becomes an opaque
// Internal status, and the caller logs the cause.
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
	info := &errdetails.ErrorInfo{Reason: e.Code, Domain: errorDomain}
	if withDetails, err := s.WithDetails(info); err == nil {
		s = withDetails
	}
	return s
}

// FromStatus converts an error returned by a client call back into an
// apperr error, so that a downstream service's code reaches the HTTP
// response unchanged.
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
			return apperr.Wrap(err, kind, info.GetReason(), s.Message())
		}
	}
	if kind, ok := codeToKind[s.Code()]; ok && kind == apperr.KindUnavailable {
		return apperr.Unavailable(err)
	}
	return apperr.Internal(err)
}
