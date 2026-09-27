package application

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
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
	mu         sync.Mutex
	challenges map[string]domain.Challenge
	tickets    map[string]ticketRow
	identities []domain.Identity
	events     []proto.Message
}

type ticketRow struct {
	domain.Ticket
	consumed bool
}

func newMemStore() *memStore {
	return &memStore{challenges: map[string]domain.Challenge{}, tickets: map[string]ticketRow{}}
}

func (s *memStore) Tx(_ context.Context, fn func(ports.Repos) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.copyState()
	if err := fn(memRepos{s}); err != nil {
		s.challenges, s.tickets, s.identities, s.events = snapshot.challenges, snapshot.tickets, snapshot.identities, snapshot.events
		return err
	}
	return nil
}

// memState is the part of memStore a transaction may roll back.
type memState struct {
	challenges map[string]domain.Challenge
	tickets    map[string]ticketRow
	identities []domain.Identity
	events     []proto.Message
}

func (s *memStore) copyState() memState {
	c := memState{challenges: map[string]domain.Challenge{}, tickets: map[string]ticketRow{}}
	for k, v := range s.challenges {
		c.challenges[k] = v
	}
	for k, v := range s.tickets {
		c.tickets[k] = v
	}
	c.identities = append([]domain.Identity(nil), s.identities...)
	c.events = append([]proto.Message(nil), s.events...)
	return c
}

func (s *memStore) Read() ports.Repos { return memRepos{s} }

type memRepos struct{ s *memStore }

func (r memRepos) Challenges() ports.ChallengeRepo { return memChallenges(r) }
func (r memRepos) Tickets() ports.TicketRepo       { return memTickets(r) }
func (r memRepos) Identities() ports.IdentityRepo  { return memIdentities(r) }

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
	return out, nil
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

type portsRepos = ports.Repos
