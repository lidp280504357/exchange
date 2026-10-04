// Package grpcapi serves auth-service's gRPC API to other services.
package grpcapi

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	"github.com/skill/exchange/internal/auth/application"
	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Server implements authv1.AuthServiceServer.
type Server struct {
	authv1.UnimplementedAuthServiceServer
	accounts *application.AccountService
}

// NewServer returns the gRPC API over accounts.
func NewServer(accounts *application.AccountService) *Server { return &Server{accounts: accounts} }

// GetContacts returns a user's verified identities.
func (s *Server) GetContacts(ctx context.Context, req *authv1.GetContactsRequest) (*authv1.GetContactsResponse, error) {
	if req.GetUserId() == "" {
		return nil, apperr.Invalid("user_id is required")
	}
	ids, err := s.accounts.Contacts(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	resp := &authv1.GetContactsResponse{}
	for _, id := range ids {
		ch := domain.ChannelEmail
		if id.Kind == domain.ChannelSMS.Kind() {
			ch = domain.ChannelSMS
		}
		resp.Contacts = append(resp.Contacts, &authv1.Contact{Channel: string(ch), Value: id.Value})
	}
	return resp, nil
}

// ConsumeStepUp redeems a step-up token for another service.
func (s *Server) ConsumeStepUp(ctx context.Context, req *authv1.ConsumeStepUpRequest) (*authv1.ConsumeStepUpResponse, error) {
	if req.GetUserId() == "" {
		return nil, apperr.Invalid("user_id is required")
	}
	su, sec, err := s.accounts.ConsumeStepUp(ctx, req.GetUserId(), req.GetToken())
	if err != nil {
		return nil, err
	}
	return &authv1.ConsumeStepUpResponse{SessionId: su.SessionID, Channel: string(su.Channel), Security: securityProto(sec)}, nil
}

// GetSecurityContext returns a user's security context without a step-up.
func (s *Server) GetSecurityContext(ctx context.Context, req *authv1.GetSecurityContextRequest) (*authv1.GetSecurityContextResponse, error) {
	sec, err := s.accounts.SecurityContext(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &authv1.GetSecurityContextResponse{Security: securityProto(sec)}, nil
}

func securityProto(sec domain.SecurityContext) *authv1.SecurityContext {
	return &authv1.SecurityContext{
		Identities: int32(sec.Identities), TotpEnabled: sec.TOTPEnabled, DeviceId: sec.DeviceID, //nolint:gosec // a handful
		DeviceFirstSeenAt: stamp(sec.DeviceFirstSeenAt), IdentityChangedAt: stamp(sec.IdentityChangedAt),
		PasswordChangedAt: stamp(sec.PasswordChangedAt), TotpChangedAt: stamp(sec.TOTPChangedAt), TotpActivatedAt: stamp(sec.TOTPActivatedAt),
	}
}

// FindUser looks a user up by email address or phone number.
func (s *Server) FindUser(ctx context.Context, req *authv1.FindUserRequest) (*authv1.FindUserResponse, error) {
	id, err := s.accounts.FindUser(ctx, req.GetIdentifier())
	if err != nil {
		return nil, err
	}
	return &authv1.FindUserResponse{UserId: id}, nil
}

func stamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// GetSecurity returns an account's security for the admin console.
func (s *Server) GetSecurity(ctx context.Context, req *authv1.GetSecurityRequest) (*authv1.GetSecurityResponse, error) {
	sec, err := s.accounts.AdminSecurity(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	out := &authv1.GetSecurityResponse{
		TotpStatus: sec.TOTP, TotpActivatedAt: stamp(sec.TOTPActivated), PasswordChangedAt: stamp(sec.Credential.PasswordChangedAt),
		LastLoginAt: stamp(sec.Credential.LastLoginAt), LockedSeconds: int32(sec.LockedFor.Seconds()), //nolint:gosec // at most 15 minutes
		PendingIdentityRequests: int32(sec.PendingRequests), //nolint:gosec // a handful
		TotpChangedAt:           stamp(sec.Credential.TOTPChangedAt),
	}
	for _, id := range sec.Identities {
		out.Identities = append(out.Identities, &authv1.IdentityInfo{
			Kind: id.Kind, Value: id.Value, VerifiedAt: stamp(id.VerifiedAt), CreatedAt: stamp(id.CreatedAt),
		})
	}
	for _, x := range sec.Sessions {
		out.Sessions = append(out.Sessions, &authv1.SessionInfo{
			Id: x.ID, DeviceId: x.DeviceID, ClientType: x.ClientType, UserAgent: x.UserAgent, Ip: x.IP, CreatedAt: stamp(x.CreatedAt),
			LastSeenAt: stamp(x.LastSeenAt),
		})
	}
	for _, d := range sec.Devices {
		out.Devices = append(out.Devices, &authv1.DeviceInfo{DeviceId: d.DeviceID, FirstSeenAt: stamp(d.FirstSeenAt), LastSeenAt: stamp(d.LastSeenAt)})
	}
	return out, nil
}

// ListLoginHistory pages through a user's sign-ins.
func (s *Server) ListLoginHistory(ctx context.Context, req *authv1.ListLoginHistoryRequest) (*authv1.ListLoginHistoryResponse, error) {
	if req.GetUserId() == "" {
		return nil, apperr.Invalid("user_id is required")
	}
	events, next, err := s.accounts.History(ctx, req.GetUserId(), req.GetBeforeId(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := &authv1.ListLoginHistoryResponse{NextBeforeId: next}
	for _, e := range events {
		out.Entries = append(out.Entries, &authv1.LoginEntry{
			Id: e.ID, Method: e.Method, Result: e.Result, IdentityMask: e.IdentityMask, DeviceId: e.DeviceID, UserAgent: e.UserAgent, Ip: e.IP,
			NewDevice: e.NewDevice, CreatedAt: stamp(e.CreatedAt),
		})
	}
	return out, nil
}

// RevokeSessions ends a user's sessions for an administrator.
func (s *Server) RevokeSessions(ctx context.Context, req *authv1.RevokeSessionsRequest) (*authv1.RevokeSessionsResponse, error) {
	n, err := s.accounts.AdminRevokeSessions(ctx, req.GetUserId(), req.GetSessionId(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &authv1.RevokeSessionsResponse{Revoked: int32(n)}, nil //nolint:gosec // at most ten
}

// ResetTOTP removes a user's authenticator app for an administrator.
func (s *Server) ResetTOTP(ctx context.Context, req *authv1.ResetTOTPRequest) (*authv1.ResetTOTPResponse, error) {
	removed, err := s.accounts.AdminResetTOTP(ctx, req.GetUserId(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &authv1.ResetTOTPResponse{Removed: removed}, nil
}

// SetTemporaryPassword gives a user a temporary password for an administrator.
func (s *Server) SetTemporaryPassword(ctx context.Context, req *authv1.SetTemporaryPasswordRequest) (*authv1.SetTemporaryPasswordResponse, error) {
	pw, n, err := s.accounts.AdminTemporaryPassword(ctx, req.GetUserId(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &authv1.SetTemporaryPasswordResponse{Password: pw, SessionsRevoked: int32(n)}, nil //nolint:gosec // at most ten
}

func identityRequest(r application.IdentityRequest) *authv1.IdentityRequest {
	return &authv1.IdentityRequest{
		Id: r.ID, UserId: r.UserID, Kind: r.Kind, NewValue: r.NewValue, CurrentValue: r.Current, Status: r.Status, CreatedAt: stamp(r.CreatedAt),
		DecidedAt: stamp(r.DecidedAt), DecidedBy: r.DecidedBy, Reason: r.Reason,
	}
}

// ListIdentityRequests pages through the rebind requests.
func (s *Server) ListIdentityRequests(ctx context.Context, req *authv1.ListIdentityRequestsRequest) (*authv1.ListIdentityRequestsResponse, error) {
	list, next, err := s.accounts.AdminIdentityRequests(ctx, req.GetStatus(), req.GetUserId(), req.GetCursor(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := &authv1.ListIdentityRequestsResponse{NextCursor: next}
	for _, r := range list {
		out.Requests = append(out.Requests, identityRequest(r))
	}
	return out, nil
}

// DecideIdentityRequest approves or rejects a rebind request.
func (s *Server) DecideIdentityRequest(ctx context.Context, req *authv1.DecideIdentityRequestRequest) (*authv1.DecideIdentityRequestResponse, error) {
	r, err := s.accounts.AdminDecideIdentityRequest(ctx, req.GetId(), req.GetApprove(), req.GetActor(), req.GetReason())
	if err != nil {
		return nil, err
	}
	return &authv1.DecideIdentityRequestResponse{Request: identityRequest(r)}, nil
}
