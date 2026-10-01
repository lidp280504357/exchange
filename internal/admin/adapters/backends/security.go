package backends

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	riskv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/risk/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/admin/ports"
)

func timeOf(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime()
}

// Security implements ports.AccountSecurity over auth-service.
type Security struct{ C authv1.AuthServiceClient }

// Get returns an account's sign-in security.
func (s Security) Get(ctx context.Context, userID string) (ports.Security, error) {
	resp, err := s.C.GetSecurity(ctx, &authv1.GetSecurityRequest{UserId: userID})
	if err != nil {
		return ports.Security{}, err
	}
	out := ports.Security{
		TOTP: resp.GetTotpStatus(), TOTPActivatedAt: timeOf(resp.GetTotpActivatedAt()), PasswordChangedAt: timeOf(resp.GetPasswordChangedAt()),
		LastLoginAt: timeOf(resp.GetLastLoginAt()), LockedSeconds: int(resp.GetLockedSeconds()),
		PendingIdentityRequests: int(resp.GetPendingIdentityRequests()),
	}
	for _, id := range resp.GetIdentities() {
		out.Identities = append(out.Identities, ports.Identity{
			Kind: id.GetKind(), Value: id.GetValue(), VerifiedAt: timeOf(id.GetVerifiedAt()), CreatedAt: timeOf(id.GetCreatedAt()),
		})
	}
	for _, x := range resp.GetSessions() {
		out.Sessions = append(out.Sessions, ports.LiveSession{
			ID: x.GetId(), DeviceID: x.GetDeviceId(), ClientType: x.GetClientType(), UserAgent: x.GetUserAgent(), IP: x.GetIp(),
			CreatedAt: timeOf(x.GetCreatedAt()), LastSeenAt: timeOf(x.GetLastSeenAt()),
		})
	}
	for _, d := range resp.GetDevices() {
		out.Devices = append(out.Devices, ports.Device{ID: d.GetDeviceId(), FirstSeenAt: timeOf(d.GetFirstSeenAt()), LastSeenAt: timeOf(d.GetLastSeenAt())})
	}
	return out, nil
}

// LoginHistory pages through an account's sign-ins.
func (s Security) LoginHistory(ctx context.Context, userID string, beforeID int64, limit int) ([]ports.LoginEntry, int64, error) {
	resp, err := s.C.ListLoginHistory(ctx, &authv1.ListLoginHistoryRequest{
		UserId: userID, BeforeId: beforeID, Limit: int32(min(limit, 200)), //nolint:gosec // bounded
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]ports.LoginEntry, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		out = append(out, ports.LoginEntry{
			ID: e.GetId(), Method: e.GetMethod(), Result: e.GetResult(), IdentityMask: e.GetIdentityMask(), DeviceID: e.GetDeviceId(),
			UserAgent: e.GetUserAgent(), IP: e.GetIp(), NewDevice: e.GetNewDevice(), CreatedAt: timeOf(e.GetCreatedAt()),
		})
	}
	return out, resp.GetNextBeforeId(), nil
}

// RevokeSessions ends an account's sessions.
func (s Security) RevokeSessions(ctx context.Context, userID, sessionID, actor, reason string) (int, error) {
	resp, err := s.C.RevokeSessions(ctx, &authv1.RevokeSessionsRequest{UserId: userID, SessionId: sessionID, Actor: actor, Reason: reason})
	if err != nil {
		return 0, err
	}
	return int(resp.GetRevoked()), nil
}

// ResetTOTP removes an account's authenticator app.
func (s Security) ResetTOTP(ctx context.Context, userID, actor, reason string) (bool, error) {
	resp, err := s.C.ResetTOTP(ctx, &authv1.ResetTOTPRequest{UserId: userID, Actor: actor, Reason: reason})
	if err != nil {
		return false, err
	}
	return resp.GetRemoved(), nil
}

// TemporaryPassword sets a temporary password on an account.
func (s Security) TemporaryPassword(ctx context.Context, userID, actor, reason string) (string, int, error) {
	resp, err := s.C.SetTemporaryPassword(ctx, &authv1.SetTemporaryPasswordRequest{UserId: userID, Actor: actor, Reason: reason})
	if err != nil {
		return "", 0, err
	}
	return resp.GetPassword(), int(resp.GetSessionsRevoked()), nil
}

func identityRequestOf(r *authv1.IdentityRequest) ports.IdentityRequest {
	return ports.IdentityRequest{
		ID: r.GetId(), UserID: r.GetUserId(), Kind: r.GetKind(), NewValue: r.GetNewValue(), CurrentValue: r.GetCurrentValue(),
		Status: r.GetStatus(), CreatedAt: timeOf(r.GetCreatedAt()), DecidedAt: timeOf(r.GetDecidedAt()), DecidedBy: r.GetDecidedBy(),
		Reason: r.GetReason(),
	}
}

// IdentityRequests pages through rebind requests, newest first.
func (s Security) IdentityRequests(ctx context.Context, q ports.IdentityRequestQuery) ([]ports.IdentityRequest, string, error) {
	resp, err := s.C.ListIdentityRequests(ctx, &authv1.ListIdentityRequestsRequest{
		Status: q.Status, UserId: q.UserID, Cursor: q.Cursor, Limit: int32(min(q.Limit, 200)), //nolint:gosec // bounded
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]ports.IdentityRequest, 0, len(resp.GetRequests()))
	for _, r := range resp.GetRequests() {
		out = append(out, identityRequestOf(r))
	}
	return out, resp.GetNextCursor(), nil
}

// DecideIdentityRequest approves or rejects a rebind request.
func (s Security) DecideIdentityRequest(ctx context.Context, id string, approve bool, actor, reason string) (ports.IdentityRequest, error) {
	resp, err := s.C.DecideIdentityRequest(ctx, &authv1.DecideIdentityRequestRequest{Id: id, Approve: approve, Actor: actor, Reason: reason})
	if err != nil {
		return ports.IdentityRequest{}, err
	}
	return identityRequestOf(resp.GetRequest()), nil
}

// History returns an account's status changes and consents.
func (u Users) History(ctx context.Context, userID string) ([]ports.StatusChange, []ports.Consent, error) {
	resp, err := u.User.GetUserHistory(ctx, &userv1.GetUserHistoryRequest{UserId: userID})
	if err != nil {
		return nil, nil, err
	}
	changes := make([]ports.StatusChange, 0, len(resp.GetStatusChanges()))
	for _, c := range resp.GetStatusChanges() {
		changes = append(changes, ports.StatusChange{
			From: c.GetFromStatus(), To: c.GetToStatus(), Reason: c.GetReasonCode(), Actor: c.GetActor(), At: timeOf(c.GetAt()),
		})
	}
	consents := make([]ports.Consent, 0, len(resp.GetConsents()))
	for _, c := range resp.GetConsents() {
		consents = append(consents, ports.Consent{Document: c.GetDocument(), Version: c.GetVersion(), AcceptedAt: timeOf(c.GetAcceptedAt())})
	}
	return changes, consents, nil
}

// Risk implements ports.Risk over risk-service.
type Risk struct{ C riskv1.RiskServiceClient }

// Assessments returns an account's newest assessments.
func (r Risk) Assessments(ctx context.Context, userID string, limit int) ([]ports.Assessment, error) {
	resp, err := r.C.ListAssessments(ctx, &riskv1.ListAssessmentsRequest{UserId: userID, Limit: int32(min(limit, 200))}) //nolint:gosec // bounded
	if err != nil {
		return nil, err
	}
	out := make([]ports.Assessment, 0, len(resp.GetAssessments()))
	for _, a := range resp.GetAssessments() {
		hits := make([]ports.RuleHit, 0, len(a.GetHits()))
		for _, h := range a.GetHits() {
			hits = append(hits, ports.RuleHit{Rule: h.GetRule(), Score: int(h.GetScore()), Detail: h.GetDetail()})
		}
		out = append(out, ports.Assessment{
			ID: a.GetId(), SourceEventType: a.GetSourceEventType(), Score: int(a.GetScore()), Action: a.GetAction(), Hits: hits,
			Enforced: a.GetEnforced(), CreatedAt: timeOf(a.GetCreatedAt()),
		})
	}
	return out, nil
}
