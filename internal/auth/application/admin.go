package application

import (
	"context"
	"crypto/rand"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	"github.com/skill/exchange/internal/auth/domain"
	"github.com/skill/exchange/internal/auth/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pagecursor"
	"github.com/skill/exchange/internal/platform/pii"
)

// What the admin console reads and does on an account's security (design
// 2026-10-02 §4.1), over gRPC on the internal network. The console checks
// the administrator's role and audits each action; auth-service records
// the effect (sessions revoked with reason ADMIN, the events users are
// told about).

// Security is an account's security as the admin console shows it.
type Security struct {
	Identities []domain.Identity
	// TOTP is "ACTIVE", "PENDING" or "" (none).
	TOTP          string
	TOTPActivated time.Time
	Credential    domain.Credential
	// LockedFor is how long password sign-in stays locked (the failures
	// per identity), zero when it is not.
	LockedFor time.Duration
	Sessions  []domain.Session
	Devices   []domain.Device
	// PendingRequests counts the identity rebind requests waiting.
	PendingRequests int
}

// AdminSecurity reads an account's identities, authenticator, password,
// lock, live sessions and devices. Session addresses are masked.
func (s *AccountService) AdminSecurity(ctx context.Context, userID string) (Security, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return Security{}, apperr.NotFound("no such user")
	}
	r := s.Store.Read()
	var out Security
	var err error
	if out.Identities, err = r.Identities().ByUser(ctx, userID); err != nil {
		return Security{}, err
	}
	if t, err := r.TOTP().Get(ctx, userID); err != nil {
		return Security{}, err
	} else if t != nil {
		out.TOTP, out.TOTPActivated = t.Status, t.ActivatedAt
	}
	if c, err := r.Credentials().Get(ctx, userID); err != nil {
		return Security{}, err
	} else if c != nil {
		out.Credential = *c
		out.Credential.PasswordHash = ""
	}
	for _, id := range out.Identities {
		fails, ttl, err := s.Guard.Failures(ctx, loginKey(id.Value))
		if err != nil {
			return Security{}, apperr.Unavailable(err)
		}
		if fails >= domain.LockAfterFailures && ttl > out.LockedFor {
			out.LockedFor = ttl
		}
	}
	if out.Sessions, err = r.Sessions().Active(ctx, userID); err != nil {
		return Security{}, err
	}
	for i := range out.Sessions {
		out.Sessions[i].IP = pii.MaskIP(out.Sessions[i].IP)
	}
	if out.Devices, err = r.Devices().List(ctx, userID); err != nil {
		return Security{}, err
	}
	if out.PendingRequests, err = r.RebindRequests().CountPending(ctx, userID); err != nil {
		return Security{}, err
	}
	return out, nil
}

// loginKey is the password-failure counter of an identity (LoginPassword).
func loginKey(value string) string { return "login_fail:" + hashKey(value) }

func needActorReason(actor, reason string) error {
	if strings.TrimSpace(actor) == "" {
		return apperr.Invalid("the actor is required")
	}
	if len(strings.TrimSpace(reason)) < 3 {
		return apperr.Invalid("a reason of at least 3 characters is required")
	}
	return nil
}

// AdminRevokeSessions ends one of a user's live sessions, or all of them
// when sessionID is empty (reason ADMIN), and returns how many ended.
func (s *AccountService) AdminRevokeSessions(ctx context.Context, userID, sessionID, actor, reason string) (int, error) {
	if err := needActorReason(actor, reason); err != nil {
		return 0, err
	}
	if _, err := uuid.Parse(userID); err != nil {
		return 0, apperr.NotFound("no such user")
	}
	var revoked []string
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if sessionID == "" {
			return s.revokeAll(ctx, r, userID, "", domain.RevokeAdmin, &revoked)
		}
		if _, err := uuid.Parse(sessionID); err != nil {
			return apperr.NotFound("no such session")
		}
		sess, err := r.Sessions().Get(ctx, sessionID)
		if err != nil {
			return err
		}
		if sess == nil || sess.UserID != userID || !sess.RevokedAt.IsZero() {
			return apperr.NotFound("no such session")
		}
		return s.revoke(ctx, r, userID, sessionID, domain.RevokeAdmin, &revoked)
	})
	s.markRevoked(ctx, revoked)
	if err != nil {
		return 0, err
	}
	s.Log.InfoContext(ctx, "sessions revoked by an administrator", "user_id", userID, "sessions", len(revoked), "actor", actor)
	return len(revoked), nil
}

// AdminResetTOTP removes a user's authenticator app (bound or being set
// up); the user is told by mail (TotpDisabled), and withdrawals wait for
// review for a day after a bound one is removed. It reports whether
// there was one.
func (s *AccountService) AdminResetTOTP(ctx context.Context, userID, actor, reason string) (bool, error) {
	if err := needActorReason(actor, reason); err != nil {
		return false, err
	}
	if _, err := uuid.Parse(userID); err != nil {
		return false, apperr.NotFound("no such user")
	}
	var removed bool
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		t, err := r.TOTP().GetForUpdate(ctx, userID)
		if err != nil || t == nil {
			return err
		}
		if err := r.TOTP().Delete(ctx, userID); err != nil {
			return err
		}
		removed = true
		if t.Status != domain.TOTPActive {
			return nil // a binding never confirmed protected nothing
		}
		if err := r.Credentials().TOTPChanged(ctx, userID, s.Now()); err != nil {
			return err
		}
		return r.Emit(ctx, &authv1.TotpDisabled{UserId: userID}, "user", userID)
	})
	if err == nil && removed {
		s.Log.InfoContext(ctx, "authenticator removed by an administrator", "user_id", userID, "actor", actor)
	}
	return removed, err
}

// temporaryAlphabet leaves out characters people misread (0/O, 1/l/I).
const temporaryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

// newTemporaryPassword returns four groups of four random characters.
func newTemporaryPassword() (string, error) {
	var b strings.Builder
	size := big.NewInt(int64(len(temporaryAlphabet)))
	for i := range 16 {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, size)
		if err != nil {
			return "", err
		}
		b.WriteByte(temporaryAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// AdminTemporaryPassword gives a user a random temporary password for an
// administrator to pass on: every session ends (reason ADMIN), the
// password lock is cleared, and the user is told by mail as for a reset
// (PasswordChanged, which also holds withdrawals for review for a day).
// The password is returned once and never logged or stored in the clear.
func (s *AccountService) AdminTemporaryPassword(ctx context.Context, userID, actor, reason string) (string, int, error) {
	if err := needActorReason(actor, reason); err != nil {
		return "", 0, err
	}
	if _, err := uuid.Parse(userID); err != nil {
		return "", 0, apperr.NotFound("no such user")
	}
	now := s.Now()
	var (
		password string
		revoked  []string
		ids      []domain.Identity
	)
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cred, err := r.Credentials().Get(ctx, userID)
		if err != nil {
			return err
		}
		if cred == nil {
			return apperr.NotFound("no such user")
		}
		if ids, err = r.Identities().ByUser(ctx, userID); err != nil {
			return err
		}
		values := make([]string, len(ids))
		for i, id := range ids {
			values[i] = id.Value
		}
		// A random password all but never fails the policy; draw again if it does.
		for range 10 {
			if password, err = newTemporaryPassword(); err != nil {
				return err
			}
			if domain.CheckPassword(password, values...) == nil {
				break
			}
		}
		if err := s.setPassword(ctx, r, userID, password, now); err != nil {
			return err
		}
		if err := s.revokeAll(ctx, r, userID, "", domain.RevokeAdmin, &revoked); err != nil {
			return err
		}
		return r.Emit(ctx, &authv1.PasswordChanged{UserId: userID, ViaReset: true}, "user", userID)
	})
	s.markRevoked(ctx, revoked)
	if err != nil {
		return "", 0, err
	}
	for _, id := range ids {
		if err := s.Guard.Clear(ctx, loginKey(id.Value)); err != nil {
			s.Log.WarnContext(ctx, "clearing the login lock failed", "user_id", userID, "error", err)
		}
	}
	s.Log.InfoContext(ctx, "temporary password set by an administrator", "user_id", userID, "sessions", len(revoked), "actor", actor)
	return password, len(revoked), nil
}

// IdentityRequest is a rebind request with the identity it replaces.
type IdentityRequest struct {
	domain.RebindRequest
	// Current is the identity's value now ("" when it is gone).
	Current string
}

// AdminIdentityRequests returns a page of rebind requests in a status
// ("": all) of a user ("": all), newest first, and the cursor of the next.
func (s *AccountService) AdminIdentityRequests(ctx context.Context, status, userID, cursor string, limit int) ([]IdentityRequest, string, error) {
	switch status {
	case "", domain.RebindPending, domain.RebindApproved, domain.RebindRejected:
	default:
		return nil, "", apperr.Invalid("status must be PENDING_REVIEW, APPROVED or REJECTED")
	}
	at, id, err := pagecursor.Decode(cursor)
	if err != nil {
		return nil, "", apperr.Invalid("bad cursor")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	r := s.Store.Read()
	list, err := r.RebindRequests().List(ctx, status, userID, at, id, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		last := list[limit-1]
		next = pagecursor.Encode(last.CreatedAt, last.ID)
	}
	out := make([]IdentityRequest, 0, len(list))
	current := map[string][]domain.Identity{}
	for _, rr := range list {
		ids, ok := current[rr.UserID]
		if !ok {
			if ids, err = r.Identities().ByUser(ctx, rr.UserID); err != nil {
				return nil, "", err
			}
			current[rr.UserID] = ids
		}
		v := IdentityRequest{RebindRequest: rr}
		for _, x := range ids {
			if x.Kind == rr.Kind {
				v.Current = x.Value
			}
		}
		out = append(out, v)
	}
	return out, next, nil
}

// AdminDecideIdentityRequest approves (the identity takes the new value,
// IdentityRebound tells the user) or rejects a pending rebind request.
func (s *AccountService) AdminDecideIdentityRequest(ctx context.Context, id string, approve bool, actor, reason string) (IdentityRequest, error) {
	if err := needActorReason(actor, reason); err != nil {
		return IdentityRequest{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return IdentityRequest{}, apperr.NotFound("no such request")
	}
	now := s.Now()
	var out IdentityRequest
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		rr, err := r.RebindRequests().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if rr == nil {
			return apperr.NotFound("no such request")
		}
		if rr.Status != domain.RebindPending {
			return apperr.New(apperr.KindConflict, apperr.CodeConflict, "the request was already decided")
		}
		ids, err := r.Identities().ByUser(ctx, rr.UserID)
		if err != nil {
			return err
		}
		var current *domain.Identity
		for i := range ids {
			if ids[i].Kind == rr.Kind {
				current = &ids[i]
			}
		}
		rr.Status, rr.DecidedAt, rr.DecidedBy, rr.Reason = domain.RebindRejected, now, actor, strings.TrimSpace(reason)
		if approve {
			if current == nil {
				return apperr.New(apperr.KindConflict, apperr.CodeConflict, "the identity to replace is gone")
			}
			if taken, err := r.Identities().Find(ctx, rr.Kind, rr.NewValue); err != nil {
				return err
			} else if taken != nil {
				return domain.ErrIdentityTaken
			}
			if err := r.Identities().UpdateValue(ctx, current.ID, rr.NewValue, now); err != nil {
				return err
			}
			rr.Status = domain.RebindApproved
			if err := r.Emit(ctx, &authv1.IdentityRebound{
				UserId: rr.UserID, Channel: string(domain.ChannelFor(rr.Kind)), OldMask: pii.MaskIdentifier(current.Value),
				NewMask: pii.MaskIdentifier(rr.NewValue),
			}, "user", rr.UserID); err != nil {
				return err
			}
		}
		if err := r.RebindRequests().Decide(ctx, *rr); err != nil {
			return err
		}
		out = IdentityRequest{RebindRequest: *rr}
		if current != nil {
			out.Current = current.Value
			if approve {
				out.Current = rr.NewValue
			}
		}
		return nil
	})
	return out, err
}
