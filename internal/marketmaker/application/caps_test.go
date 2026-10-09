package application

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	marketv1 "github.com/skill/exchange/api/gen/go/exchange/market/v1"
	"github.com/skill/exchange/internal/marketmaker/domain"
	"github.com/skill/exchange/internal/platform/apperr"
)

// memCaps is a CapsStore in memory.
type memCaps struct {
	mu      sync.Mutex
	stored  *domain.StoredCaps
	changes []domain.CapsRecord
}

func (m *memCaps) Caps(context.Context) (domain.StoredCaps, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stored == nil {
		return domain.StoredCaps{}, false, nil
	}
	return *m.stored, true, nil
}

func (m *memCaps) Seed(_ context.Context, caps domain.Caps, actor string, at time.Time) (domain.StoredCaps, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stored == nil {
		m.stored = &domain.StoredCaps{Caps: caps, Version: 1, UpdatedBy: actor, UpdatedAt: at}
		m.changes = append(m.changes, domain.CapsRecord{Version: 1, Caps: caps, Actor: actor, At: at})
	}
	return *m.stored, nil
}

func (m *memCaps) Change(_ context.Context, c domain.CapsChange, at time.Time) (domain.StoredCaps, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stored == nil || m.stored.Version != c.Version {
		return domain.StoredCaps{}, domain.ErrCapsVersion
	}
	prev := m.stored.Caps
	caps, err := c.Next(prev)
	if err != nil {
		return domain.StoredCaps{}, err
	}
	m.stored = &domain.StoredCaps{Caps: caps, Version: c.Version + 1, UpdatedBy: c.Actor, UpdatedAt: at}
	m.changes = append(m.changes, domain.CapsRecord{Version: m.stored.Version, Caps: caps, Previous: &prev, Actor: c.Actor, Reason: c.Reason, At: at})
	return *m.stored, nil
}

func (m *memCaps) Changes(_ context.Context, limit int) ([]domain.CapsRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.CapsRecord
	for i := len(m.changes) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.changes[i])
	}
	return out, nil
}

// HOUSE's caps at runtime (review C45): the environment's are stored first
// and used; a change on the stored version is stored and used from the
// next round (a level cap of 0.5 BTC's worth shows in the levels); one on
// a stale version is refused; a change made elsewhere arrives with the
// next read.
func TestRuntimeCaps(t *testing.T) {
	p, rec, _, _ := newRig(t)
	ctx := context.Background()
	store := &memCaps{}
	caps := NewCaps(store, p, slog.New(slog.DiscardHandler))
	env := DefaultConfig().Caps
	if err := caps.Start(ctx, env); err != nil {
		t.Fatal(err)
	}
	if got := caps.Get(); got.Version != 1 || !got.Caps.Level.Equal(env.Level) {
		t.Fatalf("seeded %+v", got)
	}
	// A second start (a restart) keeps what is stored.
	other := env
	other.Level = d("1")
	if err := caps.Start(ctx, other); err != nil || !caps.Get().Caps.Level.Equal(env.Level) {
		t.Fatalf("restarted: %+v %v", caps.Get(), err)
	}

	p.OnSnapshot(&marketv1.DepthSnapshot{Symbol: "BTC-USDT", Sequence: 10, Reference: true, Bids: levels("50000", "3"), Asks: levels("50001", "3")})
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 1 || books[0].GetAsks()[0].GetQuantity() != "0.3999" {
		t.Fatalf("20,000 a level: %v", books)
	}

	bigger := domain.CapsPatch{Level: ptr(d("25000.5"))}
	if _, err := caps.Change(ctx, domain.CapsChange{Patch: bigger, Version: 3, Actor: "a", Reason: "r"}); apperr.From(err).Code != "HOUSE_CAPS_VERSION" {
		t.Fatalf("a stale version: %v", err)
	}
	if _, err := caps.Change(ctx, domain.CapsChange{Patch: bigger, Version: 1, Reason: "r"}); apperr.From(err).Code != "COMMON_INVALID_ARGUMENT" {
		t.Fatalf("no actor: %v", err)
	}
	stored, err := caps.Change(ctx, domain.CapsChange{Patch: bigger, Version: 1, Actor: "admin:a", Approver: "admin:b", ApprovalID: "ap1", Reason: "deeper levels"})
	if err != nil || stored.Version != 2 || caps.Get().Version != 2 {
		t.Fatalf("changed: %+v %v", stored, err)
	}
	_ = p.publish(ctx, p.round())
	if _, books := rec.take(t); len(books) != 1 || books[0].GetAsks()[0].GetQuantity() != "0.5" {
		t.Fatalf("25,000.5 a level: %v", books)
	}

	// Another instance changes them: the next read brings them.
	elsewhere := domain.CapsPatch{Contract: ptr(d("70000"))}
	if _, err := store.Change(ctx, domain.CapsChange{Patch: elsewhere, Version: 2, Actor: "elsewhere", Reason: "r"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	s, _, _ := store.Caps(ctx)
	caps.apply(ctx, s)
	if got := caps.Get(); got.Version != 3 || !got.Caps.Contract.Equal(d("70000")) || !got.Caps.Level.Equal(d("25000.5")) {
		t.Fatalf("read again: %+v", got)
	}
	list, err := caps.Changes(ctx, 0)
	if err != nil || len(list) != 3 || list[0].Version != 3 || list[2].Previous != nil {
		t.Fatalf("changes %+v %v", list, err)
	}
}

func ptr(v decimal.Decimal) *decimal.Decimal { return &v }

// brokenSpecs cannot read the specs.
type brokenSpecs struct{ specList }

func (brokenSpecs) Specs(context.Context) ([]domain.Spec, error) {
	return nil, errors.New("instrument-service down")
}

// A new contract leverage goes up to the highest leverage of the contracts
// HOUSE quotes, read from their specs as the change is made (review C73:
// leverage per contract as Binance has it, 150 for BTC and ETH since
// 2026-10-10); without the specs nothing changes it.
func TestTheContractLeverageFollowsTheSpecs(t *testing.T) {
	p, _, _, _ := newRig(t)
	ctx := context.Background()
	perp := perpSpec
	perp.MaxLeverage = 150
	coin := domain.Spec{
		Symbol: "BTC-USD-PERP", Base: "BTC", Quote: "USD", TickSize: d("0.1"), LotSize: d("1"), Contract: true,
		Settle: "BTC", ContractSize: d("100"), MaxLeverage: 125,
	}
	p.specs = specList{btcSpec, perp, coin}
	caps := NewCaps(&memCaps{}, p, slog.New(slog.DiscardHandler))
	if err := caps.Start(ctx, DefaultConfig().Caps); err != nil {
		t.Fatal(err)
	}
	change := func(leverage string) error {
		_, err := caps.Change(ctx, domain.CapsChange{
			Patch: domain.CapsPatch{ContractLeverage: ptr(d(leverage))}, Version: caps.Get().Version, Actor: "a", Reason: "r",
		})
		return err
	}
	if e := apperr.From(change("151")); e.Code != "COMMON_INVALID_ARGUMENT" || e.Details["max_leverage"] != "150" {
		t.Fatalf("151x past the specs' 150x: %v", e)
	}
	if err := change("100"); err != nil {
		t.Fatalf("100x: %v", err)
	}
	if err := change("150"); err != nil || !caps.Get().Caps.ContractLeverage.Equal(d("150")) {
		t.Fatalf("150x: %+v %v", caps.Get(), err)
	}
	p.specs = brokenSpecs{}
	if e := apperr.From(change("120")); e.Kind != apperr.KindUnavailable {
		t.Fatalf("without the specs: %v", e)
	}
	// The other caps change without them.
	if _, err := caps.Change(ctx, domain.CapsChange{
		Patch: domain.CapsPatch{Safety: ptr(d("2000"))}, Version: caps.Get().Version, Actor: "a", Reason: "r",
	}); err != nil {
		t.Fatalf("another cap: %v", err)
	}
}
