package application

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/user/domain"
	"github.com/lidp280504357/exchange/internal/user/ports"
)

type memStore struct {
	mu        sync.Mutex
	users     map[string]domain.User
	changes   []domain.StatusChange
	events    []proto.Message
	handled   map[string]bool // inbox: consumer/event ID
	favorites map[string][]string
}

func (s *memStore) Tx(_ context.Context, fn func(ports.Repos) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	users, changes, events := maps.Clone(s.users), slices.Clone(s.changes), slices.Clone(s.events)
	if err := fn(memRepos{s}); err != nil {
		s.users, s.changes, s.events = users, changes, events
		return err
	}
	return nil
}

func (s *memStore) Once(ctx context.Context, consumer, eventID string, fn func(ports.Repos) error) (bool, error) {
	key, ran := consumer+"/"+eventID, false
	err := s.Tx(ctx, func(r ports.Repos) error {
		if s.handled[key] {
			return nil
		}
		if err := fn(r); err != nil {
			return err
		}
		if s.handled == nil {
			s.handled = map[string]bool{}
		}
		s.handled[key], ran = true, true
		return nil
	})
	return ran, err
}

func (s *memStore) Read() ports.Repos { return memRepos{s} }

type memRepos struct{ s *memStore }

func (r memRepos) Users() ports.UserRepo { return memUsers(r) }

func (r memRepos) Favorites() ports.FavoriteRepo { return memFavorites(r) }

type memFavorites memRepos

func (r memFavorites) Get(_ context.Context, userID string) ([]string, time.Time, error) {
	list, ok := r.s.favorites[userID]
	if !ok {
		return []string{}, time.Time{}, nil
	}
	return list, time.Unix(1, 0), nil
}

func (r memFavorites) Set(_ context.Context, userID string, symbols []string) (time.Time, error) {
	if r.s.favorites == nil {
		r.s.favorites = map[string][]string{}
	}
	r.s.favorites[userID] = symbols
	return time.Unix(1, 0), nil
}

func (r memRepos) Emit(_ context.Context, _ string, msg proto.Message, _, _ string) error {
	r.s.events = append(r.s.events, msg)
	return nil
}

type memUsers memRepos

func (r memUsers) Create(_ context.Context, u domain.User, _ []domain.Consent) (bool, error) {
	if _, ok := r.s.users[u.ID]; ok {
		return false, nil
	}
	u.Version = 1
	r.s.users[u.ID] = u
	return true, nil
}

func (r memUsers) Get(_ context.Context, id string) (domain.User, error) {
	u, ok := r.s.users[id]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return u, nil
}

func (r memUsers) GetForUpdate(ctx context.Context, id string) (domain.User, error) {
	return r.Get(ctx, id)
}

func (r memUsers) Update(_ context.Context, u domain.User) (domain.User, error) {
	u.Version++
	r.s.users[u.ID] = u
	return u, nil
}

func (r memUsers) AddStatusChange(_ context.Context, c domain.StatusChange) error {
	r.s.changes = append(r.s.changes, c)
	return nil
}

func (r memUsers) List(_ context.Context, f ports.UserFilter) ([]domain.User, error) {
	var out []domain.User
	for _, u := range r.s.users {
		if (f.Status == "" || u.Status == f.Status) && (f.AfterID == "" || u.CreatedAt.Before(f.AfterTime) ||
			(u.CreatedAt.Equal(f.AfterTime) && u.ID < f.AfterID)) {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b domain.User) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out[:min(f.Limit, len(out))], nil
}

func (r memUsers) Stats(_ context.Context, since time.Time, _ int) (ports.UserStats, error) {
	st := ports.UserStats{Total: int64(len(r.s.users)), Days: map[string]int64{}}
	for _, u := range r.s.users {
		if !u.CreatedAt.Before(since) {
			st.CreatedSince++
		}
	}
	return st, nil
}

func (r memUsers) StatusHistory(_ context.Context, userID string, _ int) ([]domain.StatusChange, error) {
	var out []domain.StatusChange
	for i := len(r.s.changes) - 1; i >= 0; i-- {
		if r.s.changes[i].UserID == userID {
			out = append(out, r.s.changes[i])
		}
	}
	return out, nil
}

type fakeFlags map[string]flags.Flag

func (f fakeFlags) Get(k string) (flags.Flag, bool) { fl, ok := f[k]; return fl, ok }

type fakeStepUps struct{ valid map[string]bool }

func (f *fakeStepUps) Consume(_ context.Context, _, token string) error {
	if !f.valid[token] {
		return apperr.New(apperr.KindForbidden, "AUTH_STEP_UP_REQUIRED", "step up")
	}
	delete(f.valid, token)
	return nil
}

func newService() (*Service, *memStore, *fakeStepUps) {
	store := &memStore{users: map[string]domain.User{}}
	steps := &fakeStepUps{valid: map[string]bool{}}
	return &Service{
		Store: store, Flags: fakeFlags{flags.KeyTransfer: {Enabled: true}}, StepUps: steps,
		Now: func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
	}, store, steps
}

func eventsOf[T proto.Message](s *memStore) []T {
	var out []T
	for _, e := range s.events {
		if t, ok := e.(T); ok {
			out = append(out, t)
		}
	}
	return out
}

func TestCreateAndStatusChanges(t *testing.T) {
	svc, store, _ := newService()
	ctx := context.Background()
	id := uuid.NewString()
	in := CreateInput{UserID: id, Region: "SG", TermsVersion: "v1", RiskVersion: "v1"}
	if _, err := svc.Create(ctx, in); err != nil {
		t.Fatal(err)
	}
	if u, err := svc.Create(ctx, in); err != nil || u.Version != 1 {
		t.Fatalf("repeat: %+v %v", u, err)
	}
	if _, err := svc.Create(ctx, CreateInput{UserID: uuid.NewString(), Region: "SG"}); err == nil {
		t.Fatal("consents are required")
	}

	if ok, reason, _ := svc.CheckEligibility(ctx, id, domain.FeatureTransfer, "", ""); !ok || reason != "" {
		t.Fatalf("active transfer: %v %s", ok, reason)
	}
	c, err := svc.ChangeStatus(ctx, id, "FROZEN", "SUSPICIOUS_LOGIN", "cli:ops", "ticket 42")
	if err != nil || c.From != domain.StatusActive || c.To != domain.StatusFrozen {
		t.Fatalf("freeze: %+v %v", c, err)
	}
	if ok, reason, _ := svc.CheckEligibility(ctx, id, domain.FeatureTransfer, "", ""); ok || reason != domain.ReasonFrozen {
		t.Fatalf("frozen transfer: %v %s", ok, reason)
	}
	_, err = svc.ChangeStatus(ctx, id, "CLOSED", "USER_REQUEST", "cli:ops", "")
	if !apperr.Is(err, "USER_STATUS_TRANSITION_INVALID") {
		t.Fatalf("frozen -> closed: %v", err)
	}
	if _, err := svc.ChangeStatus(ctx, id, "ACTIVE", "REVIEW_CLEARED", "cli:ops", ""); err != nil {
		t.Fatal(err)
	}

	changed := eventsOf[*userv1.UserStatusChanged](store)
	audits := eventsOf[*auditv1.AdminActionPerformed](store)
	if len(changed) != 2 || changed[0].GetToStatus() != domain.StatusFrozen || changed[0].GetActor() != "cli:ops" || len(audits) != 2 ||
		audits[0].GetTarget() != "user:"+id || audits[0].GetReason() != "SUSPICIOUS_LOGIN" {
		t.Fatalf("events: %v %v", changed, audits)
	}
	history, _ := svc.StatusHistory(ctx, id)
	if len(history) != 2 || history[0].To != domain.StatusActive {
		t.Fatalf("history: %+v", history)
	}
	if _, err := svc.ChangeStatus(ctx, uuid.NewString(), "FROZEN", "SUSPICIOUS_LOGIN", "cli:ops", ""); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	if _, _, err := svc.CheckEligibility(ctx, id, "MINING", "", ""); err == nil {
		t.Fatal("unknown feature accepted")
	}
}

func TestUpdateProfile(t *testing.T) {
	svc, store, steps := newService()
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := svc.Create(ctx, CreateInput{UserID: id, Region: "SG", TermsVersion: "v1", RiskVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	lang := "en"
	u, err := svc.UpdateProfile(ctx, id, domain.ProfilePatch{Language: &lang}, "")
	if err != nil || u.Language != "en" || u.Version != 2 {
		t.Fatalf("language: %+v %v", u, err)
	}
	code := "Blue42"
	_, err = svc.UpdateProfile(ctx, id, domain.ProfilePatch{AntiPhishingCode: &code}, "")
	if !apperr.Is(err, "AUTH_STEP_UP_REQUIRED") {
		t.Fatalf("code without step-up: %v", err)
	}
	// A malformed code fails before the step-up token is spent.
	steps.valid["su"] = true
	bad := "!!"
	if _, err := svc.UpdateProfile(ctx, id, domain.ProfilePatch{AntiPhishingCode: &bad}, "su"); err == nil || !steps.valid["su"] {
		t.Fatalf("malformed code: %v, token kept %v", err, steps.valid["su"])
	}
	if u, err = svc.UpdateProfile(ctx, id, domain.ProfilePatch{AntiPhishingCode: &code}, "su"); err != nil || u.AntiPhishingCode != "Blue42" {
		t.Fatalf("code: %+v %v", u, err)
	}
	updates := eventsOf[*userv1.ProfileUpdated](store)
	if len(updates) != 2 || updates[1].GetFields()[0] != "anti_phishing_code" {
		t.Fatalf("ProfileUpdated: %v", updates)
	}
	if u, err = svc.UpdateProfile(ctx, id, domain.ProfilePatch{Language: &lang}, ""); err != nil || len(eventsOf[*userv1.ProfileUpdated](store)) != 2 {
		t.Fatalf("no-op patch must not emit: %v", err)
	}
}

func TestRiskReviewsMoveActiveAccountsOnce(t *testing.T) {
	svc, store, _ := newService()
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := svc.Create(ctx, CreateInput{UserID: id, Region: "AQ", TermsVersion: "v1", RiskVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	event := uuid.NewString()
	if err := svc.OnRiskAction(ctx, event, id, []string{"registration_burst_device"}); err != nil {
		t.Fatal(err)
	}
	u, _ := svc.Get(ctx, id)
	changed := eventsOf[*userv1.UserStatusChanged](store)
	if u.Status != domain.StatusRiskReview || len(changed) != 1 || changed[0].GetReasonCode() != ReasonRiskRule ||
		changed[0].GetActor() != "risk-service" {
		t.Fatalf("review: %s %v", u.Status, changed)
	}
	history, _ := svc.StatusHistory(ctx, id)
	if len(history) != 1 || history[0].Note != "rules: registration_burst_device" {
		t.Fatalf("history: %+v", history)
	}

	// An operator clears the review; the same event redelivered must not reopen it.
	if _, err := svc.ChangeStatus(ctx, id, domain.StatusActive, "REVIEW_CLEARED", "cli:ops", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.OnRiskAction(ctx, event, id, []string{"registration_burst_device"}); err != nil {
		t.Fatal(err)
	}
	if u, _ := svc.Get(ctx, id); u.Status != domain.StatusActive {
		t.Fatalf("redelivery reopened the review: %s", u.Status)
	}

	// Frozen accounts stay frozen; unknown users are skipped.
	if _, err := svc.ChangeStatus(ctx, id, domain.StatusFrozen, "SUSPICIOUS_LOGIN", "cli:ops", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.OnRiskAction(ctx, uuid.NewString(), id, []string{"r"}); err != nil {
		t.Fatal(err)
	}
	if u, _ := svc.Get(ctx, id); u.Status != domain.StatusFrozen {
		t.Fatalf("frozen account moved to %s", u.Status)
	}
	if err := svc.OnRiskAction(ctx, uuid.NewString(), uuid.NewString(), []string{"r"}); err != nil {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestFavoritesAreCheckedAndStored(t *testing.T) {
	svc, _, _ := newService()
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := svc.Create(ctx, CreateInput{UserID: id, Region: "SG", TermsVersion: "v1", RiskVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	if list, at, err := svc.Favorites(ctx, id); err != nil || len(list) != 0 || !at.IsZero() {
		t.Fatalf("before: %v %v %v", list, at, err)
	}
	list, _, err := svc.SetFavorites(ctx, id, []string{"eth-usdt", "BTC-USDT", "ETH-USDT"})
	if err != nil || !slices.Equal(list, []string{"ETH-USDT", "BTC-USDT"}) {
		t.Fatalf("set: %v %v", list, err)
	}
	if got, at, _ := svc.Favorites(ctx, id); !slices.Equal(got, list) || at.IsZero() {
		t.Fatalf("stored %v at %v", got, at)
	}
	if _, _, err := svc.SetFavorites(ctx, id, []string{"not a symbol"}); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("bad symbol: %v", err)
	}
	if _, _, err := svc.SetFavorites(ctx, uuid.NewString(), nil); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestListUsersPages(t *testing.T) {
	svc, store, _ := newService()
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		id := uuid.NewString()
		store.users[id] = domain.User{ID: id, Status: domain.StatusActive, Region: "SG", CreatedAt: base.Add(time.Duration(i) * time.Hour)}
	}
	first, err := svc.ListUsers(ctx, ports.UserFilter{Limit: 2}, "")
	if err != nil || len(first.Users) != 2 || first.Next == "" || !first.Users[0].CreatedAt.Equal(base.Add(4*time.Hour)) {
		t.Fatalf("first page %+v %v", first, err)
	}
	second, _ := svc.ListUsers(ctx, ports.UserFilter{Limit: 2}, first.Next)
	third, _ := svc.ListUsers(ctx, ports.UserFilter{Limit: 2}, second.Next)
	if len(second.Users) != 2 || len(third.Users) != 1 || third.Next != "" || !third.Users[0].CreatedAt.Equal(base) {
		t.Fatalf("pages %+v %+v", second, third)
	}
	if _, err := svc.ListUsers(ctx, ports.UserFilter{Status: "GONE"}, ""); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("bad status: %v", err)
	}
	if _, err := svc.ListUsers(ctx, ports.UserFilter{}, "not-a-cursor"); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("bad cursor: %v", err)
	}
	st, err := svc.UserStats(ctx, base.Add(3*time.Hour), 7)
	if err != nil || st.Total != 5 || st.CreatedSince != 2 {
		t.Fatalf("stats %+v %v", st, err)
	}
}
