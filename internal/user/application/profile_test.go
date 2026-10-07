package application

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/user/domain"
)

// fakeAvatars keeps avatars by path; Put fails for an upload of "bad".
type fakeAvatars struct {
	stored  map[string]bool
	n       int
	removed []string
}

func (f *fakeAvatars) Put(_ context.Context, userID string, upload []byte, at time.Time) (domain.Avatar, error) {
	if string(upload) == "bad" {
		return domain.Avatar{}, domain.ErrAvatarInvalid
	}
	f.n++
	a := domain.Avatar{
		Path: fmt.Sprintf("%s/p%d.webp", userID, f.n), ThumbPath: fmt.Sprintf("%s/p%d_64.webp", userID, f.n),
		UploadedAt: at, Size: int64(len(upload)),
	}
	f.stored[a.Path] = true
	return a, nil
}

func (f *fakeAvatars) Remove(a domain.Avatar) error {
	delete(f.stored, a.Path)
	f.removed = append(f.removed, a.Path)
	return nil
}

func signUp(t *testing.T, svc *Service) domain.User {
	t.Helper()
	u, err := svc.Create(context.Background(), CreateInput{UserID: uuid.NewString(), Region: "SG", TermsVersion: "v1", RiskVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// A sign-up draws user_ and 8 lowercase letters or digits; the user may
// change it to a free, well-formed, unreserved name once in 7 days (the
// same name changes nothing); an operator's reset draws a new one and
// starts no wait (design 2026-10-07, avatars and usernames §1.1, §1.6).
func TestUsernames(t *testing.T) {
	svc, store, _ := newService()
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	u := signUp(t, svc)
	if !regexp.MustCompile(`^user_[a-z0-9]{8}$`).MatchString(u.Username) || !u.UsernameChangedAt.IsZero() {
		t.Fatalf("drawn %q at %v", u.Username, u.UsernameChangedAt)
	}
	other := signUp(t, svc)
	if other.Username == u.Username {
		t.Fatal("two sign-ups drew the same name")
	}

	for _, bad := range []string{
		"ab", "_abc", "has space", "x23456789012345678901", "名字", "admin", "Admin_2", "astras_official", "my_support",
		"house", "HOUSE", "Root",
	} {
		if _, err := svc.ChangeUsername(ctx, u.ID, bad); apperr.From(err).Code != "USER_USERNAME_INVALID" {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := svc.ChangeUsername(ctx, other.ID, "Satoshi_N"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangeUsername(ctx, u.ID, "satoshi_n"); apperr.From(err).Code != "USER_USERNAME_TAKEN" {
		t.Fatalf("taken in another case: %v", err)
	}
	got, err := svc.ChangeUsername(ctx, u.ID, "lighthouse_7")
	if err != nil || got.Username != "lighthouse_7" || !got.UsernameChangedAt.Equal(now) {
		t.Fatalf("changed %+v %v", got, err)
	}
	updated := eventsOf[*userv1.ProfileUpdated](store)
	audits := eventsOf[*auditv1.AdminActionPerformed](store)
	if len(updated) == 0 || updated[len(updated)-1].GetFields()[0] != "username" ||
		audits[len(audits)-1].GetAction() != "user.username_changed" || audits[len(audits)-1].GetActor() != "user:"+u.ID {
		t.Fatalf("events %v %v", updated, audits)
	}
	if _, err := svc.ChangeUsername(ctx, u.ID, "lighthouse_7"); err != nil {
		t.Fatalf("the same name: %v", err)
	}
	_, err = svc.ChangeUsername(ctx, u.ID, "Lighthouse_7")
	if e := apperr.From(err); e.Code != "USER_USERNAME_COOLDOWN" || e.Details["next_change_at"] != "2026-10-14T08:00:00Z" {
		t.Fatalf("within 7 days, another case: %v", err)
	}
	now = now.Add(domain.UsernameCooldown)
	if got, err = svc.ChangeUsername(ctx, u.ID, "Lighthouse_7"); err != nil || got.Username != "Lighthouse_7" {
		t.Fatalf("after 7 days: %+v %v", got, err)
	}

	got, previous, err := svc.ResetUsername(ctx, u.ID, "ops@example.com", "an offensive name")
	if err != nil || previous != "Lighthouse_7" || !regexp.MustCompile(`^user_[a-z0-9]{8}$`).MatchString(got.Username) || !got.UsernameChangedAt.IsZero() {
		t.Fatalf("reset %+v %q %v", got, previous, err)
	}
	resets := eventsOf[*userv1.ProfileReset](store)
	audits = eventsOf[*auditv1.AdminActionPerformed](store)
	if len(resets) != 1 || resets[0].GetField() != ResetUsername || resets[0].GetUsername() != got.Username ||
		audits[len(audits)-1].GetAction() != "user.username_reset" || audits[len(audits)-1].GetReason() != "an offensive name" {
		t.Fatalf("reset events %v %v", resets, audits)
	}
	if _, err := svc.ChangeUsername(ctx, u.ID, "picked_again"); err != nil {
		t.Fatalf("no wait after a reset: %v", err)
	}
	if _, _, err := svc.ResetUsername(ctx, u.ID, "", "r"); err == nil {
		t.Fatal("a reset without an actor")
	}
}

// An upload becomes the avatar and the previous one's files go; deleting
// goes back to the default; an operator's reset tells the user only when
// there was one (design §1.2, §1.6).
func TestAvatars(t *testing.T) {
	svc, store, _ := newService()
	ctx := context.Background()
	files := &fakeAvatars{stored: map[string]bool{}}
	u := signUp(t, svc)
	if _, err := svc.UploadAvatar(ctx, u.ID, []byte("png")); err == nil {
		t.Fatal("an upload without a directory")
	}
	svc.Avatars = files

	if _, err := svc.UploadAvatar(ctx, u.ID, make([]byte, domain.MaxAvatarBytes+1)); !errors.Is(err, domain.ErrAvatarTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	if _, err := svc.UploadAvatar(ctx, u.ID, []byte("bad")); !errors.Is(err, domain.ErrAvatarInvalid) {
		t.Fatalf("bad: %v", err)
	}
	if _, err := svc.UploadAvatar(ctx, uuid.NewString(), []byte("png")); !errors.Is(err, domain.ErrUserNotFound) || files.n != 0 {
		t.Fatalf("unknown user: %v, %d stored", err, files.n)
	}
	first, err := svc.UploadAvatar(ctx, u.ID, []byte("png"))
	if err != nil || first.Avatar == nil || first.Avatar.URL() != "/uploads/avatars/"+u.ID+"/p1.webp" {
		t.Fatalf("uploaded %+v %v", first.Avatar, err)
	}
	second, err := svc.UploadAvatar(ctx, u.ID, []byte("jpeg"))
	if err != nil || second.Avatar.Path == first.Avatar.Path || files.stored[first.Avatar.Path] || !files.stored[second.Avatar.Path] {
		t.Fatalf("again %+v %v %v", second.Avatar, err, files.stored)
	}
	updated := eventsOf[*userv1.ProfileUpdated](store)
	if updated[len(updated)-1].GetFields()[0] != "avatar" {
		t.Fatalf("events %v", updated)
	}
	gone, err := svc.DeleteAvatar(ctx, u.ID)
	if err != nil || gone.Avatar != nil || files.stored[second.Avatar.Path] {
		t.Fatalf("deleted %+v %v", gone.Avatar, err)
	}
	if _, removed, err := svc.ResetAvatar(ctx, u.ID, "ops@example.com", "r"); err != nil || removed {
		t.Fatalf("reset without one: %v %v", removed, err)
	}
	if len(eventsOf[*userv1.ProfileReset](store)) != 0 {
		t.Fatal("told of a reset that changed nothing")
	}
	if _, err := svc.UploadAvatar(ctx, u.ID, []byte("png")); err != nil {
		t.Fatal(err)
	}
	reset, removed, err := svc.ResetAvatar(ctx, u.ID, "ops@example.com", "not allowed")
	resets := eventsOf[*userv1.ProfileReset](store)
	if err != nil || !removed || reset.Avatar != nil || len(resets) != 1 || resets[0].GetField() != ResetAvatar || len(files.stored) != 0 {
		t.Fatalf("reset %+v %v %v %v %v", reset.Avatar, removed, err, resets, files.stored)
	}
}
