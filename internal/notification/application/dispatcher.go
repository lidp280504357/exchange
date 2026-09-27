// Package application holds notification-service's use cases.
package application

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/notification/domain"
	"github.com/lidp280504357/exchange/internal/notification/ports"
)

// Retry and circuit-breaker settings (requirements §6.2).
var (
	// DefaultRetryDelays are the waits before each retry of one provider;
	// after them the next provider takes over.
	DefaultRetryDelays = []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}
	// AttemptTimeout bounds one provider call.
	AttemptTimeout = 10 * time.Second
)

const (
	breakerThreshold = 5                // consecutive failures that open a circuit
	breakerCooldown  = 60 * time.Second // how long it stays open
)

// Dispatcher sends messages through each channel's providers in order:
// the primary is retried after each delay, then the next provider takes
// over; a provider whose circuit is open is skipped. It is an app
// component: Run waits until Stop, which cancels background retries.
type Dispatcher struct {
	routes Routes
	store  ports.DeliveryStore
	log    *slog.Logger
	delays []time.Duration

	mu       sync.Mutex
	breakers map[string]*breaker

	sends   *prometheus.CounterVec
	circuit *prometheus.GaugeVec
	failed  *prometheus.CounterVec

	bg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// NewDispatcher registers the dispatcher's metrics with reg.
func NewDispatcher(routes Routes, store ports.DeliveryStore, log *slog.Logger, reg prometheus.Registerer) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		routes:   routes,
		store:    store,
		log:      log,
		delays:   DefaultRetryDelays,
		breakers: map[string]*breaker{},
		sends: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notify_sends_total",
			Help: "Provider attempts, by channel, provider and result (ok, error, skipped).",
		}, []string{"channel", "provider", "result"}),
		circuit: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "notify_provider_circuit_open",
			Help: "1 while a provider's circuit breaker is open.",
		}, []string{"provider"}),
		failed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notify_deliveries_failed_total",
			Help: "Deliveries that exhausted every provider, by channel.",
		}, []string{"channel"}),
		ctx:    ctx,
		cancel: cancel,
	}
	reg.MustRegister(d.sends, d.circuit, d.failed)
	return d
}

// Deliver records the delivery and tries the primary provider once. On
// failure it returns FAILED_RETRYING and continues in the background.
func (d *Dispatcher) Deliver(ctx context.Context, del domain.Delivery, m domain.Message) (domain.Status, error) {
	if err := d.store.CreateDelivery(ctx, del); err != nil {
		return "", err
	}
	chain := d.routes.For(m)
	if len(chain) == 0 {
		d.fail(del, domain.FailureRejected)
		return domain.StatusFailed, domain.ErrProviderUnavailable
	}
	err := d.try(ctx, del, m, chain[0])
	if err == nil {
		return domain.StatusSent, nil
	}
	next, attempt := 0, 1
	if !retryable(err) {
		next, attempt = 1, 0
	}
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		d.continueFrom(del, m, chain, next, attempt, domain.ClassOf(err))
	}()
	return domain.StatusFailedRetrying, nil
}

// continueFrom walks the remaining attempts, starting with provider index
// pi at attempt number attempt (0 is the provider's first try).
func (d *Dispatcher) continueFrom(del domain.Delivery, m domain.Message, chain []ports.Provider, pi, attempt int, class domain.FailureClass) {
	for ; pi < len(chain); pi, attempt = pi+1, 0 {
		for ; attempt <= len(d.delays); attempt++ {
			if attempt > 0 {
				select {
				case <-d.ctx.Done():
					return // shutting down: the delivery stays FAILED_RETRYING
				case <-time.After(d.delays[attempt-1]):
				}
			}
			err := d.try(d.ctx, del, m, chain[pi])
			if err == nil {
				return
			}
			class = domain.ClassOf(err)
			if !retryable(err) {
				break
			}
		}
	}
	d.fail(del, class)
}

// try makes one attempt with p and records it.
func (d *Dispatcher) try(ctx context.Context, del domain.Delivery, m domain.Message, p ports.Provider) error {
	b := d.breaker(p.Name())
	if !b.allow(time.Now()) {
		d.sends.WithLabelValues(string(del.Channel), p.Name(), "skipped").Inc()
		return &domain.SendError{Class: domain.FailureCircuitOpen, Err: errors.New("circuit open")}
	}
	actx, cancel := context.WithTimeout(ctx, AttemptTimeout)
	defer cancel()
	msgID, err := p.Send(actx, m)
	status, class := domain.StatusSent, domain.FailureClass("")
	if err != nil {
		status, class = domain.StatusFailedRetrying, domain.ClassOf(err)
		if b.failure(time.Now()) {
			d.circuit.WithLabelValues(p.Name()).Set(1)
			d.log.Warn("provider circuit opened", "provider", p.Name(), "cooldown", breakerCooldown.String())
		}
		d.sends.WithLabelValues(string(del.Channel), p.Name(), "error").Inc()
		d.log.Warn("provider send failed", "delivery_id", del.ID, "provider", p.Name(), "class", string(class), "error", err)
	} else {
		b.success()
		d.circuit.WithLabelValues(p.Name()).Set(0)
		d.sends.WithLabelValues(string(del.Channel), p.Name(), "ok").Inc()
	}
	// Record with a fresh context: the outcome matters even if the caller left.
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer rcancel()
	if rerr := d.store.RecordAttempt(rctx, del.ID, status, p.Name(), class, msgID); rerr != nil {
		d.log.Warn("record delivery attempt failed", "delivery_id", del.ID, "error", rerr)
	}
	return err
}

// fail marks the delivery failed; its DeliveryFailed event is the
// dead-letter record operators review (§6.2).
func (d *Dispatcher) fail(del domain.Delivery, class domain.FailureClass) {
	d.failed.WithLabelValues(string(del.Channel)).Inc()
	d.log.Error("delivery failed on every provider", "delivery_id", del.ID, "channel", string(del.Channel), "class", string(class))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.store.FailDelivery(ctx, del, class); err != nil {
		d.log.Error("record delivery failure failed", "delivery_id", del.ID, "error", err)
	}
}

// Run blocks until Stop.
func (d *Dispatcher) Run() error {
	<-d.ctx.Done()
	return nil
}

// Stop cancels background retries and waits for them to return.
func (d *Dispatcher) Stop(ctx context.Context) error {
	d.cancel()
	done := make(chan struct{})
	go func() {
		d.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Routes picks the providers of a message, primary first.
type Routes struct {
	Email []ports.Provider
	SMS   []ports.Provider
	// Mock takes mail to MockEmailDomains, so tests never reach a real
	// mailbox; it also records SMS while no SMS vendor is contracted.
	Mock             ports.Provider
	MockEmailDomains []string
}

// For returns the provider chain of m.
func (r Routes) For(m domain.Message) []ports.Provider {
	if m.Channel == domain.ChannelSMS {
		return r.SMS
	}
	if at := strings.LastIndexByte(m.To, '@'); at >= 0 && r.Mock != nil &&
		slices.Contains(r.MockEmailDomains, strings.ToLower(m.To[at+1:])) {
		return []ports.Provider{r.Mock}
	}
	return r.Email
}

func retryable(err error) bool {
	var se *domain.SendError
	if errors.As(err, &se) {
		return se.Retryable
	}
	return true
}

func (d *Dispatcher) breaker(name string) *breaker {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.breakers[name]
	if !ok {
		b = &breaker{}
		d.breakers[name] = b
	}
	return b
}

// breaker opens after breakerThreshold consecutive failures and lets a
// probe through after breakerCooldown.
type breaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !now.Before(b.openUntil)
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
}

// failure reports whether this failure opened the circuit.
func (b *breaker) failure(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures < breakerThreshold {
		return false
	}
	b.failures = 0
	b.openUntil = now.Add(breakerCooldown)
	return true
}
