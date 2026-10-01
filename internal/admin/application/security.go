package application

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/pii"
)

// An account's security, history and risk on its page (design 2026-10-02
// §4.1). Email addresses and phone numbers stay masked unless an
// administrator with users.contacts reveals them, which is audited; the
// security actions are audited here, auth-service records their effect.
// A temporary password is in the answer to the administrator only, never
// in a log or the audit trail.

// UserSecurity returns an account's sign-in security with its identities
// masked.
func (s *Service) UserSecurity(ctx context.Context, p Principal, userID string) (ports.Security, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return ports.Security{}, err
	}
	if err := needUser(userID); err != nil {
		return ports.Security{}, err
	}
	sec, err := s.Security.Get(ctx, userID)
	if err != nil {
		return ports.Security{}, err
	}
	for i := range sec.Identities {
		sec.Identities[i].Value = pii.MaskIdentifier(sec.Identities[i].Value)
	}
	return sec, nil
}

// RevealContacts returns an account's identities unmasked; each call is
// audited as admin.users.contacts_revealed.
func (s *Service) RevealContacts(ctx context.Context, p Principal, userID string) ([]ports.Identity, error) {
	if err := p.require(domain.PermUsersContacts); err != nil {
		return nil, err
	}
	if err := needUser(userID); err != nil {
		return nil, err
	}
	sec, err := s.Security.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	kinds := make([]string, 0, len(sec.Identities))
	for _, id := range sec.Identities {
		kinds = append(kinds, id.Kind)
	}
	details, _ := json.Marshal(map[string][]string{"kinds": kinds})
	if err := s.audit(ctx, p, "user:"+userID, "admin.users.contacts_revealed", "contacts shown", string(details)); err != nil {
		return nil, err
	}
	return sec.Identities, nil
}

// LoginHistory returns a page of an account's sign-ins, newest first, and
// the cursor of the next ("" on the last).
func (s *Service) LoginHistory(ctx context.Context, p Principal, userID, cursor string, limit int) ([]ports.LoginEntry, string, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, "", err
	}
	if err := needUser(userID); err != nil {
		return nil, "", err
	}
	var before int64
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || n <= 0 {
			return nil, "", apperr.Invalid("bad cursor")
		}
		before = n
	}
	list, next, err := s.Security.LoginHistory(ctx, userID, before, pageLimit(limit))
	if err != nil || next == 0 {
		return list, "", err
	}
	return list, strconv.FormatInt(next, 10), nil
}

// RevokeUserSessions ends one of an account's live sessions, or all of
// them when sessionID is "", audited as admin.users.sessions_revoked; it
// returns how many ended.
func (s *Service) RevokeUserSessions(ctx context.Context, p Principal, userID, sessionID, reason string) (int, error) {
	if err := s.securityAction(p, userID, reason); err != nil {
		return 0, err
	}
	sessionID = strings.TrimSpace(sessionID)
	n, err := s.Security.RevokeSessions(ctx, userID, sessionID, p.Admin.Email, reason)
	if err != nil {
		return 0, err
	}
	which := sessionID
	if which == "" {
		which = "all"
	}
	details, _ := json.Marshal(map[string]any{"session": which, "revoked": n})
	return n, s.audit(ctx, p, "user:"+userID, "admin.users.sessions_revoked", reason, string(details))
}

// ResetUserTOTP removes an account's authenticator app, audited as
// admin.users.totp_reset; false when there was none.
func (s *Service) ResetUserTOTP(ctx context.Context, p Principal, userID, reason string) (bool, error) {
	if err := s.securityAction(p, userID, reason); err != nil {
		return false, err
	}
	removed, err := s.Security.ResetTOTP(ctx, userID, p.Admin.Email, reason)
	if err != nil {
		return false, err
	}
	details, _ := json.Marshal(map[string]bool{"removed": removed})
	return removed, s.audit(ctx, p, "user:"+userID, "admin.users.totp_reset", reason, string(details))
}

// TemporaryPassword gives an account a random password for the
// administrator to pass on (every session ends, the password lock is
// cleared), audited as admin.users.password_reset without the password.
// It returns the password and how many sessions ended.
func (s *Service) TemporaryPassword(ctx context.Context, p Principal, userID, reason string) (string, int, error) {
	if err := s.securityAction(p, userID, reason); err != nil {
		return "", 0, err
	}
	pw, n, err := s.Security.TemporaryPassword(ctx, userID, p.Admin.Email, reason)
	if err != nil {
		return "", 0, err
	}
	details, _ := json.Marshal(map[string]any{"method": "TEMPORARY_PASSWORD", "sessions_revoked": n})
	if err := s.audit(ctx, p, "user:"+userID, "admin.users.password_reset", reason, string(details)); err != nil {
		// The password is set: say so instead of losing it.
		s.Log.ErrorContext(ctx, "auditing a temporary password failed", "user_id", userID, "error", err)
	}
	return pw, n, nil
}

func (s *Service) securityAction(p Principal, userID, reason string) error {
	if err := p.require(domain.PermUsersSecurity); err != nil {
		return err
	}
	if err := needUser(userID); err != nil {
		return err
	}
	return needReason(reason)
}

// UserHistory returns an account's status changes, newest first, and the
// document versions it accepted.
func (s *Service) UserHistory(ctx context.Context, p Principal, userID string) ([]ports.StatusChange, []ports.Consent, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, nil, err
	}
	if err := needUser(userID); err != nil {
		return nil, nil, err
	}
	return s.History.History(ctx, userID)
}

// UserRisk returns the risk rules' newest assessments of an account.
func (s *Service) UserRisk(ctx context.Context, p Principal, userID string, limit int) ([]ports.Assessment, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, err
	}
	if err := needUser(userID); err != nil {
		return nil, err
	}
	return s.Risk.Assessments(ctx, userID, pageLimit(limit))
}

// Identity rebind requests' statuses (auth-service).
const (
	RebindPending  = "PENDING_REVIEW"
	RebindApproved = "APPROVED"
	RebindRejected = "REJECTED"
)

func maskRequest(r ports.IdentityRequest) ports.IdentityRequest {
	r.NewValue = pii.MaskIdentifier(r.NewValue)
	if r.CurrentValue != "" {
		r.CurrentValue = pii.MaskIdentifier(r.CurrentValue)
	}
	return r
}

// IdentityRequests returns a page of identity rebind requests, newest
// first, with the values masked.
func (s *Service) IdentityRequests(ctx context.Context, p Principal, q ports.IdentityRequestQuery) ([]ports.IdentityRequest, string, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, "", err
	}
	switch q.Status {
	case "", RebindPending, RebindApproved, RebindRejected:
	default:
		return nil, "", apperr.Invalid("status must be PENDING_REVIEW, APPROVED or REJECTED")
	}
	if q.UserID != "" {
		if _, err := uuid.Parse(q.UserID); err != nil {
			return nil, "", apperr.Invalid("user_id must be a UUID")
		}
	}
	q.Limit = pageLimit(q.Limit)
	list, next, err := s.Security.IdentityRequests(ctx, q)
	if err != nil {
		return nil, "", err
	}
	for i := range list {
		list[i] = maskRequest(list[i])
	}
	return list, next, nil
}

// DecideIdentityRequest approves (the identity takes the new value) or
// rejects a pending rebind request, audited as
// admin.users.identity_request_decided.
func (s *Service) DecideIdentityRequest(ctx context.Context, p Principal, id string, approve bool, reason string) (ports.IdentityRequest, error) {
	if err := p.require(domain.PermUsersSecurity); err != nil {
		return ports.IdentityRequest{}, err
	}
	if err := needReason(reason); err != nil {
		return ports.IdentityRequest{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return ports.IdentityRequest{}, apperr.NotFound("no such request")
	}
	r, err := s.Security.DecideIdentityRequest(ctx, id, approve, p.Admin.Email, reason)
	if err != nil {
		return ports.IdentityRequest{}, err
	}
	details, _ := json.Marshal(map[string]any{"request_id": r.ID, "kind": r.Kind, "status": r.Status})
	return maskRequest(r), s.audit(ctx, p, "user:"+r.UserID, "admin.users.identity_request_decided", reason, string(details))
}
