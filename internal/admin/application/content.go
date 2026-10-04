package application

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Operations content (design 2026-10-02 §4.5): announcements and help
// articles kept by notification-service and read by the sites, written and
// published here (content.write, audited on <section>:<slug>); in-app
// messages to one user, a tag's users or everyone (notices.send, audited
// on broadcast:<id>). Every administrator reads them.

// sections maps the console's names of the sections to the service's;
// LEGAL (terms, privacy, risk, fees, about, contact) and HOME (home-hero)
// are the fixed pages a launch overrides (design 2026-10-04 §4.4), their
// slugs checked by notification-service.
var sections = map[string]string{
	"ANNOUNCEMENT": "ANNOUNCEMENT", "ANNOUNCEMENTS": "ANNOUNCEMENT", "HELP": "HELP", "LEGAL": "LEGAL", "HOME": "HOME",
}

func section(s string) (string, error) {
	if v, ok := sections[strings.ToUpper(strings.TrimSpace(s))]; ok {
		return v, nil
	}
	return "", apperr.Invalid("section must be ANNOUNCEMENT, HELP, LEGAL or HOME")
}

// Articles returns a section's articles in every status.
func (s *Service) Articles(ctx context.Context, _ Principal, sec string) (json.RawMessage, error) {
	sec, err := section(sec)
	if err != nil {
		return nil, err
	}
	return s.Content.Articles(ctx, sec)
}

// Article returns one article with every text.
func (s *Service) Article(ctx context.Context, _ Principal, id string) (json.RawMessage, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such article")
	}
	return s.Content.Article(ctx, id)
}

// auditArticle records a change of an article on <section>:<slug> (a slug
// may hold a test and a live page: the detail says which).
func (s *Service) auditArticle(ctx context.Context, p Principal, raw json.RawMessage, action, reason string) error {
	var a struct {
		ID        string  `json:"id"`
		Section   string  `json:"section"`
		Slug      string  `json:"slug"`
		Modes     string  `json:"modes"`
		Status    string  `json:"status"`
		Version   int     `json:"version"`
		PublishAt *string `json:"publish_at"`
	}
	_ = json.Unmarshal(raw, &a)
	d, _ := json.Marshal(map[string]any{"id": a.ID, "modes": a.Modes, "status": a.Status, "version": a.Version, "publish_at": a.PublishAt})
	return s.audit(ctx, p, strings.ToLower(a.Section)+":"+a.Slug, action, reason, string(d))
}

// articleModes checks an article's modes: TEST, FORMAL or BOTH, or left
// out (BOTH for a draft, the article's own for an edit).
func articleModes(m string) error {
	if m != "" && m != "TEST" && m != "FORMAL" && m != "BOTH" {
		return apperr.Invalid("modes is TEST, FORMAL or BOTH")
	}
	return nil
}

// CreateArticle writes a draft.
func (s *Service) CreateArticle(ctx context.Context, p Principal, a ports.ArticleWrite, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermContentEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	sec, err := section(a.Section)
	if err != nil {
		return nil, err
	}
	if err := articleModes(a.Modes); err != nil {
		return nil, err
	}
	a.Section, a.Actor = sec, p.Admin.Email
	raw, err := s.Content.CreateArticle(ctx, a)
	if err != nil {
		return nil, err
	}
	return raw, s.auditArticle(ctx, p, raw, "admin.content.created", strings.TrimSpace(reason))
}

// UpdateArticle rewrites an article at the version the console read.
func (s *Service) UpdateArticle(ctx context.Context, p Principal, id string, a ports.ArticleWrite, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermContentEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such article")
	}
	if err := articleModes(a.Modes); err != nil {
		return nil, err
	}
	a.Section, a.Actor = "", p.Admin.Email
	raw, err := s.Content.UpdateArticle(ctx, id, a)
	if err != nil {
		return nil, err
	}
	return raw, s.auditArticle(ctx, p, raw, "admin.content.updated", strings.TrimSpace(reason))
}

// PublishArticle shows an article on the sites from publishAt on (now when
// nil; within a minute, as the sites refresh).
func (s *Service) PublishArticle(ctx context.Context, p Principal, id string, version int, publishAt *time.Time, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermContentEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such article")
	}
	raw, err := s.Content.PublishArticle(ctx, id, version, publishAt, p.Admin.Email)
	if err != nil {
		return nil, err
	}
	return raw, s.auditArticle(ctx, p, raw, "admin.content.published", strings.TrimSpace(reason))
}

// ArchiveArticle takes an article off the sites.
func (s *Service) ArchiveArticle(ctx context.Context, p Principal, id string, version int, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermContentEdit); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such article")
	}
	raw, err := s.Content.ArchiveArticle(ctx, id, version, p.Admin.Email)
	if err != nil {
		return nil, err
	}
	return raw, s.auditArticle(ctx, p, raw, "admin.content.archived", strings.TrimSpace(reason))
}

// Broadcasts pages through the in-app messages sent, newest first.
func (s *Service) Broadcasts(ctx context.Context, _ Principal, cursor string, limit int) (json.RawMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.Content.Broadcasts(ctx, cursor, limit)
}

// Broadcast returns a message sent with how many it reached and how many
// read it.
func (s *Service) Broadcast(ctx context.Context, _ Principal, id string) (json.RawMessage, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such message")
	}
	return s.Content.Broadcast(ctx, id)
}

// ResumeBroadcast sends a FAILED in-app message again from where it
// stopped: its rounds failed ten times in a row (C5.5 ⑫). Audited as
// admin.notices.resumed before notification-service is asked, as a
// message is sent (C5.5 ㉓); a refusal is audited too
// (admin.notices.resume_failed), so no resume goes unrecorded.
func (s *Service) ResumeBroadcast(ctx context.Context, p Principal, id, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermNoticesSend); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such message")
	}
	reason = strings.TrimSpace(reason)
	if err := s.audit(ctx, p, "broadcast:"+id, "admin.notices.resumed", reason, "{}"); err != nil {
		return nil, err
	}
	raw, err := s.Content.ResumeBroadcast(ctx, id, p.Admin.Email)
	if err != nil {
		d, _ := json.Marshal(map[string]string{"error": err.Error()})
		if aerr := s.audit(ctx, p, "broadcast:"+id, "admin.notices.resume_failed", reason, string(d)); aerr != nil {
			s.Log.ErrorContext(ctx, "the failed resume is not audited", "broadcast_id", id, "error", aerr)
		}
		return nil, err
	}
	return raw, nil
}

// Audiences of an in-app message as the console names them.
const (
	AudienceAll  = "ALL"
	AudienceUser = "USER"
	AudienceTag  = "TAG"
)

// maxTagged bounds the users a tag's message reaches.
const maxTagged = 10_000

// BroadcastInput is an in-app message as the console sends it: to
// everyone, one user or the users with a tag.
type BroadcastInput struct {
	Audience string
	UserID   string
	Tag      string
	Title    map[string]string
	Body     map[string]string
	Link     string
	Email    bool
	// Key is the request's Idempotency-Key ("" for none).
	Key string
}

// SendBroadcast sends an in-app message; notification-service delivers it
// in rounds. A tag's users are those tagged now. The message's ID comes
// from the request's key and is audited before it is sent, so the same
// request again sends nothing more and audits nothing more (C5.5 ⑥).
func (s *Service) SendBroadcast(ctx context.Context, p Principal, in BroadcastInput, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermNoticesSend); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	w := ports.BroadcastWrite{Title: in.Title, Body: in.Body, Link: in.Link, Email: in.Email, Actor: p.Admin.Email, Audience: "USERS"}
	details := map[string]any{"audience": strings.ToUpper(in.Audience), "email": in.Email, "link": in.Link, "title": in.Title["zh-CN"]}
	switch strings.ToUpper(in.Audience) {
	case AudienceAll:
		w.Audience = "ALL"
	case AudienceUser:
		if _, err := uuid.Parse(in.UserID); err != nil {
			return nil, apperr.Invalid("user_id must be a user's ID")
		}
		w.UserIDs = []string{in.UserID}
		details["user_id"] = in.UserID
	case AudienceTag:
		tags, err := domain.Tags([]string{in.Tag})
		if err != nil || len(tags) != 1 {
			return nil, apperr.Invalid("tag must be an account tag")
		}
		users, err := s.Store.Read().Tags().Users(ctx, tags[0], maxTagged+1)
		if err != nil {
			return nil, err
		}
		switch {
		case len(users) == 0:
			return nil, apperr.New(apperr.KindUnprocessable, "ADMIN_TAG_EMPTY", "no account has this tag")
		case len(users) > maxTagged:
			return nil, apperr.Invalid("a tag's message reaches at most 10,000 accounts")
		}
		w.UserIDs = users
		details["tag"], details["users"] = tags[0], len(users)
	default:
		return nil, apperr.Invalid("audience must be ALL, USER or TAG")
	}
	title, _ := json.Marshal(in.Title)
	body, _ := json.Marshal(in.Body)
	hash := fingerprint(strings.ToUpper(in.Audience), in.UserID, in.Tag, string(title), string(body), in.Link, strconv.FormatBool(in.Email),
		strings.TrimSpace(reason))
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		c, err := s.claimIn(ctx, r, p, in.Key, scopeBroadcast, hash)
		if err != nil || !c.Fresh {
			w.ID = c.Ref
			return err
		}
		w.ID = c.Ref
		d, _ := json.Marshal(details)
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "broadcast:" + w.ID, Action: "admin.notices.sent", Actor: p.Admin.Email, Reason: strings.TrimSpace(reason), Details: string(d),
		}, p.Admin.Email)
	})
	if err != nil {
		return nil, err
	}
	return s.Content.SendBroadcast(ctx, w)
}
