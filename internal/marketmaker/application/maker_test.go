package application

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/marketmaker/domain"
	"github.com/lidp280504357/exchange/internal/marketmaker/ports"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

type book struct {
	orders    map[string]ports.Order
	next      int
	placed    int
	cancelAll int
	// paused makes Place answer ports.ErrPaused.
	paused bool
}

func (b *book) Open(context.Context, string) ([]ports.Order, error) {
	var out []ports.Order
	for _, o := range b.orders {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (b *book) Place(_ context.Context, _ string, q domain.Quote) error {
	if b.paused {
		return ports.ErrPaused
	}
	b.next++
	b.placed++
	id := fmt.Sprintf("o%03d", b.next)
	b.orders[id] = ports.Order{ID: id, Side: q.Side, Price: q.Price}
	return nil
}

func (b *book) Cancel(_ context.Context, id string) error {
	delete(b.orders, id)
	return nil
}

func (b *book) CancelAll(context.Context, string) error {
	b.cancelAll++
	clear(b.orders)
	return nil
}

type wallet map[string]ports.Balance

func (w wallet) Spot(context.Context) (map[string]ports.Balance, error) { return w, nil }

type reference struct {
	price decimal.Decimal
	fresh bool
}

func (r *reference) Price(context.Context, string) (decimal.Decimal, bool, error) {
	return r.price, r.fresh, nil
}

type pairs struct{ status string }

func (p *pairs) Pair(context.Context, string) (ports.PairInfo, error) {
	return ports.PairInfo{Status: p.status, Base: "BTC", Quote: "USDT", TickSize: d("0.01"), LotSize: d("0.0001")}, nil
}

type contracts struct{ status string }

func (c *contracts) Pair(context.Context, string) (ports.PairInfo, error) {
	return ports.PairInfo{Status: c.status, Base: "BTC", Quote: "USDT", TickSize: d("0.1"), LotSize: d("0.001")}, nil
}

type position struct{ net decimal.Decimal }

func (p *position) Net(context.Context, string) (decimal.Decimal, error) { return p.net, nil }

type switchFlag struct{ on bool }

func (f *switchFlag) Enabled(key string, s flags.Subject) bool {
	return f.on && key == flags.KeyMarketMaker && (s.Symbol == "BTC-USDT" || s.Symbol == "BTC-USDT-PERP")
}

func spotOnly(p domain.Params, b *book, w wallet, ref *reference, pr *pairs, fl *switchFlag) *Maker {
	return New([]domain.Params{p}, Spot{Orders: b, Balances: w, Refs: ref, Pairs: pr}, nil, Contracts{}, fl,
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
}

func TestMakerKeepsThePairQuoted(t *testing.T) {
	ctx := context.Background()
	b := &book{orders: map[string]ports.Order{}}
	ref := &reference{price: d("80000"), fresh: true}
	pr := &pairs{status: "TRADING"}
	fl := &switchFlag{}
	w := wallet{"BTC": {Available: d("1")}, "USDT": {Available: d("80000")}}
	p := domain.Defaults("BTC-USDT")
	m := spotOnly(p, b, w, ref, pr, fl)

	step := func() {
		t.Helper()
		if err := m.Step(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	step()
	if b.placed != 0 || b.cancelAll != 1 {
		t.Fatalf("flag off: %d placed, %d pulls", b.placed, b.cancelAll)
	}
	step()
	if b.cancelAll != 1 {
		t.Fatal("pulled again for the same reason")
	}

	fl.on = true
	step()
	if len(b.orders) != 10 || b.placed != 10 {
		t.Fatalf("quoting: %d orders", len(b.orders))
	}
	step()
	if b.placed != 10 {
		t.Fatal("an unchanged reference must not requote")
	}
	// A quote was taken: only it comes back.
	delete(b.orders, "o003")
	step()
	if len(b.orders) != 10 || b.placed != 11 {
		t.Fatalf("refill: %d orders, %d placed", len(b.orders), b.placed)
	}
	// A 0.03% move keeps the quotes; a 0.1% move replaces them.
	ref.price = d("80024")
	step()
	if b.placed != 11 {
		t.Fatal("a small move must not requote")
	}
	ref.price = d("80080")
	step()
	if len(b.orders) != 10 || b.placed != 21 {
		t.Fatalf("requote: %d orders, %d placed", len(b.orders), b.placed)
	}
	for _, o := range b.orders {
		if o.Side == domain.Buy && o.Price.GreaterThanOrEqual(d("80080")) || o.Side == domain.Sell && o.Price.LessThanOrEqual(d("80080")) {
			t.Fatalf("a quote on the wrong side of the reference: %+v", o)
		}
	}
	// Stale reference, halted pair: pulled.
	ref.fresh = false
	step()
	if len(b.orders) != 0 || b.cancelAll != 2 {
		t.Fatalf("stale reference: %d orders, %d pulls", len(b.orders), b.cancelAll)
	}
	ref.fresh = true
	step()
	pr.status = "HALT"
	step()
	if len(b.orders) != 0 || b.cancelAll != 3 {
		t.Fatalf("halted: %d orders, %d pulls", len(b.orders), b.cancelAll)
	}
}

func TestMakerQuotesOnlyWhatItCanFund(t *testing.T) {
	b := &book{orders: map[string]ports.Order{}}
	// 100 USDT buys one 0.002 BTC level at 40000; no BTC to sell.
	w := wallet{"BTC": {}, "USDT": {Available: d("100")}}
	p := domain.Defaults("BTC-USDT")
	m := spotOnly(p, b, w, &reference{price: d("40000"), fresh: true}, &pairs{status: "TRADING"}, &switchFlag{on: true})
	if err := m.Step(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(b.orders) != 1 {
		t.Fatalf("%d orders, want the one level the funds cover", len(b.orders))
	}
}

func TestMakerQuotesAContractAroundItsMarkPrice(t *testing.T) {
	ctx := context.Background()
	b := &book{orders: map[string]ports.Order{}}
	mark := &reference{price: d("60000"), fresh: true}
	spec := &contracts{status: "TRADING"}
	pos := &position{}
	p := domain.Defaults("BTC-USDT-PERP")
	p.MaxBase = d("0.01")
	m := New(nil, Spot{}, []domain.Params{p}, Contracts{Orders: b, Positions: pos, Marks: mark, Specs: spec}, &switchFlag{on: true},
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	step := func() {
		t.Helper()
		if err := m.StepContract(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	step()
	if len(b.orders) != 10 {
		t.Fatalf("flat: %d quotes, want 5 a side", len(b.orders))
	}
	for _, o := range b.orders {
		if o.Side == domain.Buy && o.Price.GreaterThanOrEqual(d("60000")) || o.Side == domain.Sell && o.Price.LessThanOrEqual(d("60000")) {
			t.Fatalf("a quote on the wrong side of the mark: %+v", o)
		}
	}
	// Long at the ceiling: a requote keeps only the asks.
	pos.net = d("0.01")
	mark.price = d("60100")
	step()
	for _, o := range b.orders {
		if o.Side == domain.Buy {
			t.Fatalf("bids at the long ceiling: %+v", o)
		}
	}
	if len(b.orders) != 5 {
		t.Fatalf("long: %d quotes", len(b.orders))
	}
	// Under reduce-only the contract service refuses opening orders: every
	// quote comes off, without an error, until it takes them again.
	pos.net = decimal.Zero
	mark.price = d("60200")
	b.paused = true
	step()
	if len(b.orders) != 0 || b.cancelAll != 1 {
		t.Fatalf("paused: %d quotes, %d pulls", len(b.orders), b.cancelAll)
	}
	step()
	if b.cancelAll != 1 {
		t.Fatal("pulled again for the same reason")
	}
	b.paused = false
	step()
	if len(b.orders) != 10 {
		t.Fatalf("resumed: %d quotes", len(b.orders))
	}
	// A stale mark or a halted contract pulls the quotes too.
	mark.fresh = false
	step()
	if len(b.orders) != 0 || b.cancelAll != 2 {
		t.Fatalf("stale mark: %d quotes, %d pulls", len(b.orders), b.cancelAll)
	}
	mark.fresh = true
	spec.status = "HALT"
	step()
	if b.cancelAll != 3 {
		t.Fatalf("halted: %d pulls", b.cancelAll)
	}
}
