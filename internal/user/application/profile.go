package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/user/domain"
	"github.com/skill/exchange/internal/user/ports"
)

// Usernames and avatars (design 2026-10-07, avatars and usernames).

// The fields of a ProfileReset.
const (
	ResetUsername = "USERNAME"
	ResetAvatar   = "AVATAR"
)

// ChangeUsername gives the user the name asked for: checked, unique
// whatever the case, once in 7 days (the same name exactly changes
// nothing; in another case it is a change). Kept as an audit event.
func (s *Service) ChangeUsername(ctx context.Context, userID, name string) (domain.User, error) {
	if err := domain.CheckUsername(name); err != nil {
		return domain.User{}, err
	}
	var out domain.User
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		u, err := r.Users().GetForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if u.Username == name {
			out = u
			return nil
		}
		now := s.Now()
		if next := u.NextUsernameChange(now); !next.IsZero() {
			return domain.ErrUsernameCooldown.WithDetail("next_change_at", next.UTC().Format(time.RFC3339))
		}
		before := u.Username
		u.Username, u.UsernameChangedAt = name, now
		if out, err = r.Users().Update(ctx, u); err != nil {
			return err
		}
		if err := r.Emit(ctx, event.TopicUser, &userv1.ProfileUpdated{UserId: userID, Fields: []string{"username"}}, "user", userID); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{"from": before, "to": name})
		actor := "user:" + userID
		return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "user.username_changed", Actor: actor, Details: string(details),
		}, "actor", actor)
	})
	return out, err
}

// UploadAvatar makes an upload the user's avatar: processed and stored
// first, then recorded; the previous avatar's files are deleted once the
// new one is recorded (a failure there only leaves files nobody points
// to).
func (s *Service) UploadAvatar(ctx context.Context, userID string, upload []byte) (domain.User, error) {
	if s.Avatars == nil {
		return domain.User{}, apperr.Unavailable(errors.New("avatars are not stored here (AVATAR_DIR)"))
	}
	if len(upload) > domain.MaxAvatarBytes {
		return domain.User{}, domain.ErrAvatarTooLarge
	}
	if _, err := s.Get(ctx, userID); err != nil {
		return domain.User{}, err
	}
	a, err := s.Avatars.Put(ctx, userID, upload, s.Now())
	if err != nil {
		return domain.User{}, err
	}
	out, old, err := s.setAvatar(ctx, userID, &a, nil)
	if err != nil {
		s.remove(ctx, a)
		return domain.User{}, err
	}
	if old != nil {
		s.remove(ctx, *old)
	}
	return out, nil
}

// DeleteAvatar takes the user back to the default avatar.
func (s *Service) DeleteAvatar(ctx context.Context, userID string) (domain.User, error) {
	out, old, err := s.setAvatar(ctx, userID, nil, nil)
	if err == nil && old != nil {
		s.remove(ctx, *old)
	}
	return out, err
}

// ResetAvatar is an operator's DeleteAvatar (design §1.6): the user is told
// in the app when there was one; removed reports whether there was.
func (s *Service) ResetAvatar(ctx context.Context, userID, actor, reason string) (domain.User, bool, error) {
	if actor == "" || reason == "" {
		return domain.User{}, false, apperr.Invalid("actor and reason are required")
	}
	out, old, err := s.setAvatar(ctx, userID, nil, &userv1.ProfileReset{
		UserId: userID, Field: ResetAvatar, Actor: actor, Reason: reason,
	})
	if err != nil {
		return domain.User{}, false, err
	}
	if old != nil {
		s.remove(ctx, *old)
	}
	return out, old != nil, nil
}

// setAvatar records a (nil: the default) as the user's avatar and returns
// the previous one; reset, when given and there was one to change, goes
// out with an audit event.
func (s *Service) setAvatar(ctx context.Context, userID string, a *domain.Avatar, reset *userv1.ProfileReset) (domain.User, *domain.Avatar, error) {
	var out domain.User
	var old *domain.Avatar
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		u, err := r.Users().GetForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		old = u.Avatar
		if old == nil && a == nil {
			out = u
			return nil
		}
		u.Avatar = a
		if out, err = r.Users().Update(ctx, u); err != nil {
			return err
		}
		if err := r.Emit(ctx, event.TopicUser, &userv1.ProfileUpdated{UserId: userID, Fields: []string{"avatar"}}, "user", userID); err != nil {
			return err
		}
		if reset == nil {
			return nil
		}
		return s.emitReset(ctx, r, reset, map[string]string{"path": old.Path})
	})
	if err != nil {
		return domain.User{}, nil, err
	}
	return out, old, nil
}

// ResetUsername is an operator's change of a username to a new drawn one
// (design §1.6): the 7-day wait does not start, the user is told in the
// app; previous is the name before.
func (s *Service) ResetUsername(ctx context.Context, userID, actor, reason string) (domain.User, string, error) {
	if actor == "" || reason == "" {
		return domain.User{}, "", apperr.Invalid("actor and reason are required")
	}
	var out domain.User
	var previous string
	var err error
	for range drawAttempts {
		name := domain.DrawUsername()
		err = s.Store.Tx(ctx, func(r ports.Repos) error {
			u, err := r.Users().GetForUpdate(ctx, userID)
			if err != nil {
				return err
			}
			previous = u.Username
			u.Username, u.UsernameChangedAt = name, time.Time{}
			if out, err = r.Users().Update(ctx, u); err != nil {
				return err
			}
			if err := r.Emit(ctx, event.TopicUser, &userv1.ProfileUpdated{UserId: userID, Fields: []string{"username"}}, "user", userID); err != nil {
				return err
			}
			return s.emitReset(ctx, r, &userv1.ProfileReset{
				UserId: userID, Field: ResetUsername, Username: name, Actor: actor, Reason: reason,
			}, map[string]string{"from": previous, "to": name})
		})
		if !errors.Is(err, domain.ErrUsernameTaken) {
			break
		}
	}
	if err != nil {
		return domain.User{}, "", err
	}
	return out, previous, nil
}

// emitReset sends a ProfileReset and its audit event.
func (s *Service) emitReset(ctx context.Context, r ports.Repos, reset *userv1.ProfileReset, details map[string]string) error {
	if err := r.Emit(ctx, event.TopicUser, reset, "user", reset.GetUserId()); err != nil {
		return err
	}
	action := "user.avatar_reset"
	if reset.GetField() == ResetUsername {
		action = "user.username_reset"
	}
	d, _ := json.Marshal(details)
	return r.Emit(ctx, event.TopicAudit, &auditv1.AdminActionPerformed{
		Target: "user:" + reset.GetUserId(), Action: action, Actor: reset.GetActor(), Reason: reset.GetReason(), Details: string(d),
	}, "actor", reset.GetActor())
}

// remove deletes an avatar's files, logging a failure (the files are only
// left behind).
func (s *Service) remove(ctx context.Context, a domain.Avatar) {
	if s.Avatars == nil {
		return
	}
	if err := s.Avatars.Remove(a); err != nil {
		slog.Default().WarnContext(ctx, "avatar files not removed", "path", a.Path, "error", err)
	}
}
