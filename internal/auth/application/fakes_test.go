package application

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/auth/domain"
	"github.com/lidp280504357/exchange/internal/auth/ports"
	"github.com/lidp280504357/exchange/internal/platform/flags"
	"github.com/lidp280504357/exchange/internal/platform/ratelimit"
)

// memStore is an in-memory ports.Store whose transactions roll back on error.
type memStore struct {
	mu sync.Mutex
	memState
}

// memState is the part of memStore a transaction may roll back.
type memState struct {
	challenges  map[string]domain.Challenge
	tickets     map[string]ticketRow
	identities  []domain.Identity
	events      []proto.Message
	credentials map[string]domain.Credential
	sessions    map[string]domain.Session
	refresh     map[string]domain.RefreshToken
	loginChalls map[string]domain.LoginChallenge
	stepUps     map[string]domain.StepUp
	devices     map[string]bool
	history     []domain.LoginEvent
	rebinds     []domain.RebindRequest
}

type ticketRow struct {
	domain.Ticket
	consumed bool
}

func newMemStore() *memStore {
	return &memStore{memState: memState{
		challenges: map[string]domain.Challenge{}, tickets: map[string]ticketRow{},
		credentials: map[string]domain.Credential{}, sessions: map[string]domain.Session{},
		refresh: map[string]domain.RefreshToken{}, loginChalls: map[string]domain.LoginChallenge{},
		stepUps: map[string]domain.StepUp{}, devices: map[string]bool{},
	}}
}

func (s *memStore) Tx(_ context.Context, fn func(ports.Repos) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.clone()
	if err := fn(memRepos{s}); err != nil {
		s.memState = snapshot
		return err
	}
	return nil
}

func (m *memState) clone() memState {
	return memState{
		challenges: maps.Clone(m.challenges), tickets: maps.Clone(m.tickets),
		identities: slices.Clone(m.identities), events: slices.Clone(m.events),
		credentials: maps.Clone(m.credentials), sessions: maps.Clone(m.sessions),
		refresh: maps.Clone(m.refresh), loginChalls: maps.Clone(m.loginChalls),
		stepUps: maps.Clone(m.stepUps), devices: maps.Clone(m.devices),
		history: slices.Clone(m.history), rebinds: slices.Clone(m.rebinds),
	}
}

func (s *memStore) Read() ports.Repos { return memRepos{s} }

// eventsOf returns the emitted events of type T.
func eventsOf[T proto.Message](s *memStore) []T {
	var out []T
	for _, e := range s.events {
		if t, ok := e.(T); ok {
			out = append(out, t)
		}
	}
	return out
}

type memRepos struct{ s *memStore }

func (r memRepos) Challenges() ports.ChallengeRepo           { return memChallenges(r) }
func (r memRepos) Tickets() ports.TicketRepo                 { return memTickets(r) }
func (r memRepos) Identities() ports.IdentityRepo            { return memIdentities(r) }
func (r memRepos) Credentials() ports.CredentialRepo         { return memCredentials(r) }
func (r memRepos) Sessions() ports.SessionRepo               { return memSessions(r) }
func (r memRepos) LoginChallenges() ports.LoginChallengeRepo { return memLoginChallenges(r) }
func (r memRepos) StepUps() ports.StepUpRepo                 { return memStepUps(r) }
func (r memRepos) Devices() ports.DeviceRepo                 { return memDevices(r) }
func (r memRepos) History() ports.HistoryRepo                { return memHistory(r) }
func (r memRepos) RebindRequests() ports.RebindRepo          { return memRebinds(r) }

func (r memRepos) Emit(_ context.Context, msg proto.Message, _, _ string) error {
	r.s.events = append(r.s.events, msg)
	return nil
}

type memChallenges memRepos

func (r memChallenges) Create(_ context.Context, c *domain.Challenge) error {
	r.s.challenges[c.ID] = *c
	return nil
}

func (r memChallenges) GetForUpdate(_ context.Context, id string) (*domain.Challenge, error) {
	c, ok := r.s.challenges[id]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (r memChallenges) Update(_ context.Context, c *domain.Challenge) error {
	r.s.challenges[c.ID] = *c
	return nil
}

type memTickets memRepos

func (r memTickets) Create(_ context.Context, t domain.Ticket) error {
	r.s.tickets[hex.EncodeToString(t.Hash)] = ticketRow{Ticket: t}
	return nil
}

func (r memTickets) Consume(_ context.Context, hash []byte, scene domain.Scene, deviceID string, now time.Time) (string, error) {
	key := hex.EncodeToString(hash)
	t, ok := r.s.tickets[key]
	if !ok || t.consumed || t.Scene != scene || t.DeviceID != deviceID || !now.Before(t.ExpiresAt) || !bytes.Equal(t.Hash, hash) {
		return "", nil
	}
	t.consumed = true
	r.s.tickets[key] = t
	return t.ChallengeID, nil
}

type memIdentities memRepos

func (r memIdentities) Find(_ context.Context, kind, value string) (*domain.Identity, error) {
	for _, id := range r.s.identities {
		if id.Kind == kind && id.Value == value {
			return &id, nil
		}
	}
	return nil, nil
}

func (r memIdentities) ByUser(_ context.Context, userID string) ([]domain.Identity, error) {
	var out []domain.Identity
	for _, id := range r.s.identities {
		if id.UserID == userID {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, func(a, b domain.Identity) int { return compareStrings(a.Kind, b.Kind) })
	return out, nil
}

func (r memIdentities) Create(_ context.Context, id domain.Identity, _ time.Time) error {
	for _, x := range r.s.identities {
		if x.UserID == id.UserID && x.Kind == id.Kind {
			return domain.ErrIdentityKindBound
		}
		if x.Kind == id.Kind && x.Value == id.Value {
			return domain.ErrIdentityTaken
		}
	}
	r.s.identities = append(r.s.identities, id)
	return nil
}

func (r memIdentities) UpdateValue(_ context.Context, id, value string, _ time.Time) error {
	for i, x := range r.s.identities {
		if x.ID == id {
			r.s.identities[i].Value = value
			return nil
		}
	}
	return errors.New("no such identity")
}

type memCredentials memRepos

func (r memCredentials) Create(_ context.Context, userID, hash string, _ time.Time) error {
	if _, ok := r.s.credentials[userID]; ok {
		return errors.New("credential exists")
	}
	r.s.credentials[userID] = domain.Credential{UserID: userID, PasswordHash: hash}
	return nil
}

func (r memCredentials) Get(_ context.Context, userID string) (*domain.Credential, error) {
	c, ok := r.s.credentials[userID]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (r memCredentials) RecordLogin(_ context.Context, userID string, now time.Time) error {
	c := r.s.credentials[userID]
	c.LastLoginAt, c.FailedAttempts, c.LockedUntil = now, 0, time.Time{}
	r.s.credentials[userID] = c
	return nil
}

func (r memCredentials) RecordFailure(_ context.Context, userID string, lockedUntil time.Time) error {
	c := r.s.credentials[userID]
	c.FailedAttempts++
	if !lockedUntil.IsZero() {
		c.LockedUntil = lockedUntil
	}
	r.s.credentials[userID] = c
	return nil
}

func (r memCredentials) SetPassword(_ context.Context, userID, hash string, _ time.Time) error {
	c := r.s.credentials[userID]
	c.PasswordHash = hash
	r.s.credentials[userID] = c
	return nil
}

type memSessions memRepos

func (r memSessions) Create(_ context.Context, s domain.Session) error {
	if s.LastSeenAt.IsZero() {
		s.LastSeenAt = s.CreatedAt
	}
	r.s.sessions[s.ID] = s
	return nil
}

func (r memSessions) Get(_ context.Context, id string) (*domain.Session, error) {
	s, ok := r.s.sessions[id]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (r memSessions) Active(_ context.Context, userID string) ([]domain.Session, error) {
	var out []domain.Session
	for _, s := range r.s.sessions {
		if s.UserID == userID && s.RevokedAt.IsZero() {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b domain.Session) int {
		if c := b.LastSeenAt.Compare(a.LastSeenAt); c != 0 {
			return c
		}
		return compareStrings(b.ID, a.ID)
	})
	return out, nil
}

func (r memSessions) Touch(_ context.Context, id, ip, userAgent string, now time.Time) error {
	s := r.s.sessions[id]
	s.IP, s.UserAgent, s.LastSeenAt = ip, userAgent, now
	r.s.sessions[id] = s
	return nil
}

func (r memSessions) Revoke(_ context.Context, id, _ string, now time.Time) (bool, error) {
	s, ok := r.s.sessions[id]
	if !ok || !s.RevokedAt.IsZero() {
		return false, nil
	}
	s.RevokedAt = now
	r.s.sessions[id] = s
	return true, nil
}

func (r memSessions) CreateRefresh(_ context.Context, t domain.RefreshToken) error {
	r.s.refresh[hex.EncodeToString(t.Hash)] = t
	return nil
}

func (r memSessions) RefreshForUpdate(_ context.Context, hash []byte) (*domain.RefreshToken, error) {
	t, ok := r.s.refresh[hex.EncodeToString(hash)]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

func (r memSessions) MarkRotated(_ context.Context, hash []byte, now time.Time) error {
	key := hex.EncodeToString(hash)
	t := r.s.refresh[key]
	t.RotatedAt = now
	r.s.refresh[key] = t
	return nil
}

type memLoginChallenges memRepos

func (r memLoginChallenges) Create(_ context.Context, lc domain.LoginChallenge) error {
	r.s.loginChalls[lc.ID] = lc
	return nil
}

func (r memLoginChallenges) Get(_ context.Context, id string) (*domain.LoginChallenge, error) {
	lc, ok := r.s.loginChalls[id]
	if !ok {
		return nil, nil
	}
	return &lc, nil
}

func (r memLoginChallenges) Consume(_ context.Context, id string, now time.Time) (bool, error) {
	lc, ok := r.s.loginChalls[id]
	if !ok || !lc.ConsumedAt.IsZero() || !now.Before(lc.ExpiresAt) {
		return false, nil
	}
	lc.ConsumedAt = now
	r.s.loginChalls[id] = lc
	return true, nil
}

type memStepUps memRepos

func (r memStepUps) Create(_ context.Context, s domain.StepUp) error {
	r.s.stepUps[hex.EncodeToString(s.Hash)] = s
	return nil
}

func (r memStepUps) Consume(_ context.Context, hash []byte, userID string, now time.Time) (*domain.StepUp, error) {
	key := hex.EncodeToString(hash)
	s, ok := r.s.stepUps[key]
	if !ok || s.UserID != userID || !now.Before(s.ExpiresAt) {
		return nil, nil
	}
	delete(r.s.stepUps, key)
	return &s, nil
}

type memDevices memRepos

func (r memDevices) Seen(_ context.Context, userID, deviceID string, _ time.Time) (bool, error) {
	key := userID + "/" + deviceID
	seen := r.s.devices[key]
	r.s.devices[key] = true
	return !seen, nil
}

type memHistory memRepos

func (r memHistory) Add(_ context.Context, e domain.LoginEvent) error {
	e.ID = int64(len(r.s.history) + 1)
	r.s.history = append(r.s.history, e)
	return nil
}

func (r memHistory) List(_ context.Context, userID string, beforeID int64, limit int) ([]domain.LoginEvent, error) {
	var out []domain.LoginEvent
	for i := len(r.s.history) - 1; i >= 0 && len(out) < limit; i-- {
		e := r.s.history[i]
		if e.UserID == userID && (beforeID == 0 || e.ID < beforeID) {
			out = append(out, e)
		}
	}
	return out, nil
}

type memRebinds memRepos

func (r memRebinds) Create(_ context.Context, req domain.RebindRequest) error {
	r.s.rebinds = append(r.s.rebinds, req)
	return nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// memLimiter mirrors the Redis script: all checks pass or none counts.
type memLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (l *memLimiter) Allow(_ context.Context, checks ...ratelimit.Check) (ratelimit.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts == nil {
		l.counts = map[string]int{}
	}
	for _, c := range checks {
		if l.counts[c.Rule.Name+":"+c.Key] >= c.Rule.Limit {
			return ratelimit.Result{Rule: c.Rule, RetryAfter: c.Rule.Window}, nil
		}
	}
	for _, c := range checks {
		l.counts[c.Rule.Name+":"+c.Key]++
	}
	return ratelimit.Result{Allowed: true}, nil
}

type fakeNotifier struct {
	mu   sync.Mutex
	sent []ports.OTPDelivery
}

func (n *fakeNotifier) SendOTP(_ context.Context, d ports.OTPDelivery) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, d)
	return nil
}

func (n *fakeNotifier) last() (ports.OTPDelivery, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.sent) == 0 {
		return ports.OTPDelivery{}, false
	}
	return n.sent[len(n.sent)-1], true
}

type fakeCaptcha struct{}

func (fakeCaptcha) Verify(_ context.Context, token, _ string) error {
	if token != "human" {
		return errors.New("bot")
	}
	return nil
}

type fakeFlags map[string]flags.Flag

func (f fakeFlags) Enabled(key string, s flags.Subject) bool {
	fl, ok := f[key]
	return ok && fl.Allows(s)
}

// fakeUsers is user-service.
type fakeUsers struct {
	mu      sync.Mutex
	users   map[string]ports.UserInfo
	created []ports.NewUser
	fail    error
}

func (u *fakeUsers) Create(_ context.Context, n ports.NewUser) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.fail != nil {
		return u.fail
	}
	if u.users == nil {
		u.users = map[string]ports.UserInfo{}
	}
	if _, ok := u.users[n.ID]; !ok {
		u.users[n.ID] = ports.UserInfo{ID: n.ID, Status: domain.StatusActive, Region: n.Region, Language: n.Language}
		u.created = append(u.created, n)
	}
	return nil
}

func (u *fakeUsers) Get(_ context.Context, userID string) (ports.UserInfo, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	info, ok := u.users[userID]
	if !ok {
		return ports.UserInfo{}, errors.New("user not found")
	}
	return info, nil
}

func (u *fakeUsers) setStatus(userID, status string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	info := u.users[userID]
	info.Status = status
	u.users[userID] = info
}

// fakeTokens issues readable access tokens.
type fakeTokens struct{}

func (fakeTokens) Issue(userID, sessionID, scope string, now time.Time) (string, time.Time, error) {
	return fmt.Sprintf("access:%s:%s:%s", userID, sessionID, scope), now.Add(15 * time.Minute), nil
}

// fakeRevocations records the sessions marked for the gateway.
type fakeRevocations struct {
	mu  sync.Mutex
	ids []string
}

func (f *fakeRevocations) Revoke(_ context.Context, ids ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, ids...)
	return nil
}

func (f *fakeRevocations) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.ids, id)
}

// memGuard counts failures without expiry.
type memGuard struct {
	mu     sync.Mutex
	counts map[string]int
}

func (g *memGuard) Failures(_ context.Context, key string) (int, time.Duration, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.counts[key], domain.LockDuration, nil
}

func (g *memGuard) Fail(_ context.Context, key string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.counts == nil {
		g.counts = map[string]int{}
	}
	g.counts[key]++
	return g.counts[key], nil
}

func (g *memGuard) Clear(_ context.Context, key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.counts, key)
	return nil
}

type portsRepos = ports.Repos
