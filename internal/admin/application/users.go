package application

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pagecursor"
)

// A user's page (design 2026-10-02 §4.1): the account with the console's
// own notes and tags on it. Notes are a timeline nobody edits; tags
// (VIP, SUSPICIOUS, TEST ...) are replaced as a whole. Both are audited.

func needUser(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return apperr.NotFound("no such user")
	}
	return nil
}

// UserDetail returns an account with its tags.
func (s *Service) UserDetail(ctx context.Context, p Principal, userID string) (ports.User, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return ports.User{}, err
	}
	if err := needUser(userID); err != nil {
		return ports.User{}, err
	}
	u, err := s.Users.Get(ctx, userID)
	if err != nil {
		return ports.User{}, err
	}
	tags, err := s.Store.Read().Tags().Of(ctx, []string{userID})
	if err != nil {
		return ports.User{}, err
	}
	u.Tags = nonNil(tags[userID])
	return u, nil
}

// ResetUsername gives an account a new drawn username (design 2026-10-07,
// avatars and usernames §1.6): one person with users.status, audited as
// admin.users.username_reset with the names before and after.
func (s *Service) ResetUsername(ctx context.Context, p Principal, userID, reason string) (ports.User, error) {
	if err := s.moderation(p, userID, reason); err != nil {
		return ports.User{}, err
	}
	u, previous, err := s.Users.ResetUsername(ctx, userID, p.Admin.Email, reason)
	if err != nil {
		return ports.User{}, err
	}
	details, _ := json.Marshal(map[string]string{"from": previous, "to": u.Username})
	if err := s.audit(ctx, p, "user:"+userID, "admin.users.username_reset", reason, string(details)); err != nil {
		return ports.User{}, err
	}
	return s.tagged(ctx, u)
}

// ResetAvatar takes an account back to the default avatar: one person with
// users.status, audited as admin.users.avatar_reset.
func (s *Service) ResetAvatar(ctx context.Context, p Principal, userID, reason string) (ports.User, error) {
	if err := s.moderation(p, userID, reason); err != nil {
		return ports.User{}, err
	}
	u, removed, err := s.Users.ResetAvatar(ctx, userID, p.Admin.Email, reason)
	if err != nil {
		return ports.User{}, err
	}
	details, _ := json.Marshal(map[string]bool{"removed": removed})
	if err := s.audit(ctx, p, "user:"+userID, "admin.users.avatar_reset", reason, string(details)); err != nil {
		return ports.User{}, err
	}
	return s.tagged(ctx, u)
}

// moderation checks a reset of an account's username or avatar.
func (s *Service) moderation(p Principal, userID, reason string) error {
	if err := p.require(domain.PermUsersStatus); err != nil {
		return err
	}
	if err := needUser(userID); err != nil {
		return err
	}
	return needReason(reason)
}

// tagged is an account with the console's tags.
func (s *Service) tagged(ctx context.Context, u ports.User) (ports.User, error) {
	list, err := s.withTags(ctx, []ports.User{u})
	if err != nil {
		return ports.User{}, err
	}
	return list[0], nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// withTags fills in the tags of a page of accounts.
func (s *Service) withTags(ctx context.Context, list []ports.User) ([]ports.User, error) {
	ids := make([]string, 0, len(list))
	for _, u := range list {
		ids = append(ids, u.ID)
	}
	tags, err := s.Store.Read().Tags().Of(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Tags = nonNil(tags[list[i].ID])
	}
	return list, nil
}

// Notes returns a page of the notes on an account, newest first, and the
// cursor of the next ("" on the last).
func (s *Service) Notes(ctx context.Context, p Principal, userID, cursor string, limit int) ([]domain.Note, string, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, "", err
	}
	if err := needUser(userID); err != nil {
		return nil, "", err
	}
	at, id, err := pagecursor.Decode(cursor)
	if err != nil {
		return nil, "", apperr.Invalid("bad cursor")
	}
	limit = pageLimit(limit)
	list, err := s.Store.Read().Notes().List(ctx, userID, at, id, limit+1)
	if err != nil || len(list) <= limit {
		return list, "", err
	}
	list = list[:limit]
	last := list[limit-1]
	return list, pagecursor.Encode(last.CreatedAt, last.ID), nil
}

// AddNote writes a note on an account (audited as admin.users.note_added).
func (s *Service) AddNote(ctx context.Context, p Principal, userID, body string) (domain.Note, error) {
	if err := p.require(domain.PermUsersNotes); err != nil {
		return domain.Note{}, err
	}
	if err := needUser(userID); err != nil {
		return domain.Note{}, err
	}
	n, err := domain.NewNote(uuid.Must(uuid.NewV7()).String(), userID, p.Admin.ID, body, s.Now())
	if err != nil {
		return domain.Note{}, err
	}
	if _, err := s.Users.Get(ctx, userID); err != nil {
		return domain.Note{}, err
	}
	n.AdminEmail = p.Admin.Email
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Notes().Insert(ctx, n); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]string{"note_id": n.ID, "body": n.Body})
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "admin.users.note_added", Actor: p.Admin.Email, Reason: "note", Details: string(details),
		}, p.Admin.Email)
	})
	return n, err
}

// SetTags replaces an account's tags (audited as admin.users.tags_changed
// with the tags before and after); it returns them as stored.
func (s *Service) SetTags(ctx context.Context, p Principal, userID string, tags []string) ([]string, error) {
	if err := p.require(domain.PermUsersNotes); err != nil {
		return nil, err
	}
	if err := needUser(userID); err != nil {
		return nil, err
	}
	tags, err := domain.Tags(tags)
	if err != nil {
		return nil, err
	}
	if _, err := s.Users.Get(ctx, userID); err != nil {
		return nil, err
	}
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		before, err := r.Tags().Of(ctx, []string{userID})
		if err != nil {
			return err
		}
		if slices.Equal(before[userID], tags) {
			return nil
		}
		if err := r.Tags().Set(ctx, userID, tags, p.Admin.ID, s.Now()); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string][]string{"before": nonNil(before[userID]), "after": tags})
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "admin.users.tags_changed", Actor: p.Admin.Email,
			Reason: fmt.Sprintf("tags: %v", tags), Details: string(details),
		}, p.Admin.Email)
	})
	return tags, err
}
