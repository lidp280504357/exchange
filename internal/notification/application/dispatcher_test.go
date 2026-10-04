package application

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/notification/domain"
	"github.com/skill/exchange/internal/notification/ports"
)

// scriptedProvider fails with the scripted errors, then succeeds.
type scriptedProvider struct {
	name string
	mu   sync.Mutex
	errs []error
	sent []domain.Message
}

func (p *scriptedProvider) Name() string { return p.name }

func (p *scriptedProvider) Send(_ context.Context, m domain.Message) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		return "", err
	}
	p.sent = append(p.sent, m)
	return p.name + "-id", nil
}

func (p *scriptedProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

type memDeliveries struct {
	mu       sync.Mutex
	status   map[string]domain.Status
	provider map[string]string
	attempts map[string]int
	failed   map[string]domain.FailureClass
}

func newMemDeliveries() *memDeliveries {
	return &memDeliveries{
		status: map[string]domain.Status{}, provider: map[string]string{},
		attempts: map[string]int{}, failed: map[string]domain.FailureClass{},
	}
}

func (s *memDeliveries) CreateDelivery(_ context.Context, d domain.Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[d.ID] = domain.StatusQueued
	return nil
}

func (s *memDeliveries) RecordAttempt(_ context.Context, id string, status domain.Status, provider string, _ domain.FailureClass, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[id], s.provider[id] = status, provider
	s.attempts[id]++
	return nil
}

func (s *memDeliveries) FailDelivery(_ context.Context, d domain.Delivery, class domain.FailureClass) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[d.ID], s.failed[d.ID] = domain.StatusFailed, class
	return nil
}

func (s *memDeliveries) get(id string) (domain.Status, string, int, domain.FailureClass) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status[id], s.provider[id], s.attempts[id], s.failed[id]
}

var (
	timeout  = &domain.SendError{Class: domain.FailureTimeout, Retryable: true, Err: errors.New("timeout")}
	badEmail = &domain.SendError{Class: domain.FailureInvalidTarget, Err: errors.New("invalid address")}
)

func newDispatcher(t *testing.T, email ...*scriptedProvider) (*Dispatcher, *memDeliveries) {
	t.Helper()
	store := newMemDeliveries()
	var chain []ports.Provider
	for _, p := range email {
		chain = append(chain, p)
	}
	mock := &scriptedProvider{name: "mock"}
	d := NewDispatcher(Routes{Email: chain, SMS: []ports.Provider{mock}, Mock: mock, MockEmailDomains: []string{"example.com"}},
		store, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	d.delays = []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	return d, store
}

func deliver(t *testing.T, d *Dispatcher, id, to string) domain.Status {
	t.Helper()
	status, err := d.Deliver(context.Background(),
		domain.Delivery{ID: id, Kind: domain.KindOTP, Channel: domain.ChannelEmail, Template: "otp.register", TargetMask: "x"},
		domain.Message{Channel: domain.ChannelEmail, To: to, Subject: "s", Text: "t"})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	return status
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFirstAttemptSucceeds(t *testing.T) {
	primary := &scriptedProvider{name: "resend"}
	d, store := newDispatcher(t, primary)
	if status := deliver(t, d, "d-1", "a@mail.test"); status != domain.StatusSent {
		t.Fatalf("status = %s", status)
	}
	if st, p, n, _ := store.get("d-1"); st != domain.StatusSent || p != "resend" || n != 1 {
		t.Fatalf("record: %s %s %d", st, p, n)
	}
}

func TestRetriesThenSucceeds(t *testing.T) {
	primary := &scriptedProvider{name: "resend", errs: []error{timeout, timeout}}
	backup := &scriptedProvider{name: "backup"}
	d, store := newDispatcher(t, primary, backup)
	if status := deliver(t, d, "d-1", "a@mail.test"); status != domain.StatusFailedRetrying {
		t.Fatalf("status = %s", status)
	}
	eventually(t, func() bool { st, _, _, _ := store.get("d-1"); return st == domain.StatusSent })
	if _, p, n, _ := store.get("d-1"); p != "resend" || n != 3 || backup.count() != 0 {
		t.Fatalf("the primary should succeed on its third try: provider=%s attempts=%d backup=%d", p, n, backup.count())
	}
}

func TestFailsOverAfterRetries(t *testing.T) {
	primary := &scriptedProvider{name: "resend", errs: []error{timeout, timeout, timeout, timeout}}
	backup := &scriptedProvider{name: "backup"}
	d, store := newDispatcher(t, primary, backup)
	deliver(t, d, "d-1", "a@mail.test")
	eventually(t, func() bool { st, _, _, _ := store.get("d-1"); return st == domain.StatusSent })
	if _, p, n, _ := store.get("d-1"); p != "backup" || n != 5 {
		t.Fatalf("four primary tries, then the backup: provider=%s attempts=%d", p, n)
	}
}

func TestPermanentErrorsFailOverAtOnce(t *testing.T) {
	primary := &scriptedProvider{name: "resend", errs: []error{badEmail}}
	backup := &scriptedProvider{name: "backup"}
	d, store := newDispatcher(t, primary, backup)
	deliver(t, d, "d-1", "a@mail.test")
	eventually(t, func() bool { st, _, _, _ := store.get("d-1"); return st == domain.StatusSent })
	if _, p, n, _ := store.get("d-1"); p != "backup" || n != 2 {
		t.Fatalf("no retries of a permanent error: provider=%s attempts=%d", p, n)
	}
}

func TestExhaustedDeliveryIsDeadLettered(t *testing.T) {
	primary := &scriptedProvider{name: "resend", errs: []error{timeout, timeout, timeout, timeout}}
	backup := &scriptedProvider{name: "backup", errs: []error{badEmail}}
	d, store := newDispatcher(t, primary, backup)
	deliver(t, d, "d-1", "a@mail.test")
	eventually(t, func() bool { st, _, _, _ := store.get("d-1"); return st == domain.StatusFailed })
	if _, _, _, class := store.get("d-1"); class != domain.FailureInvalidTarget {
		t.Fatalf("the last failure class is kept: %s", class)
	}
}

func TestCircuitBreakerSkipsAFailingProvider(t *testing.T) {
	primary := &scriptedProvider{name: "resend"}
	for range breakerThreshold {
		primary.errs = append(primary.errs, badEmail)
	}
	backup := &scriptedProvider{name: "backup"}
	d, store := newDispatcher(t, primary, backup)
	for i := range breakerThreshold {
		id := "d-" + string(rune('a'+i))
		deliver(t, d, id, "a@mail.test")
		eventually(t, func() bool { st, _, _, _ := store.get(id); return st == domain.StatusSent })
	}
	// The circuit is now open: the next delivery skips the primary even
	// though it would succeed.
	deliver(t, d, "d-open", "a@mail.test")
	eventually(t, func() bool { st, _, _, _ := store.get("d-open"); return st == domain.StatusSent })
	if _, p, _, _ := store.get("d-open"); p != "backup" || primary.count() != 0 {
		t.Fatalf("open circuit must skip the primary: provider=%s primary sent=%d", p, primary.count())
	}
}

func TestTestDomainsGoToTheMock(t *testing.T) {
	primary := &scriptedProvider{name: "resend"}
	d, store := newDispatcher(t, primary)
	deliver(t, d, "d-1", "tester@Example.com")
	if _, p, _, _ := store.get("d-1"); p != "mock" || primary.count() != 0 {
		t.Fatalf("test domains never reach real providers: %s", p)
	}
}

func TestSendOTPValidatesAndMasks(t *testing.T) {
	d, store := newDispatcher(t, &scriptedProvider{name: "resend"})
	if _, _, err := d.SendOTP(context.Background(), OTPRequest{Channel: "EMAIL", Target: "a@mail.test", Code: "12ab56"}); err == nil {
		t.Fatal("codes are six digits")
	}
	if _, _, err := d.SendOTP(context.Background(), OTPRequest{Channel: "FAX", Target: "x", Code: "123456"}); err == nil {
		t.Fatal("unknown channel")
	}
	id, status, err := d.SendOTP(context.Background(), OTPRequest{Channel: "SMS", Target: "+8613812341234", Code: "123456", Scene: "LOGIN", TTLSeconds: 300})
	if err != nil || status != domain.StatusSent {
		t.Fatalf("SMS goes to the mock provider: %s %v", status, err)
	}
	if _, p, _, _ := store.get(id); p != "mock" {
		t.Fatalf("provider = %s", p)
	}
}
