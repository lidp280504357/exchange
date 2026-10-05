package application_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/adapters/postgres"
	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// balances is what one margin account holds and owes of an asset.
type balances struct{ free, locked, borrowed, interest decimal.Decimal }

// ledger is an in-memory ledger with the margin postings' semantics: one
// key books once (the same content replays, other content conflicts), a
// posting books all its moves or none, balances and debts never go below
// zero, and no transfer out takes what the asset's own debt holds.
type ledger struct {
	mu     sync.Mutex
	down   bool
	spot   map[string]map[string]decimal.Decimal              // user, asset
	margin map[string]map[domain.Account]map[string]*balances // user, account, asset
	done   map[string][32]byte
	// lent and income mirror HOUSE's side: principal lent and interest
	// received, per asset.
	lent, income map[string]decimal.Decimal
	posts        int
}

func newLedger() *ledger {
	return &ledger{
		spot: map[string]map[string]decimal.Decimal{}, margin: map[string]map[domain.Account]map[string]*balances{},
		done: map[string][32]byte{}, lent: map[string]decimal.Decimal{}, income: map[string]decimal.Decimal{},
	}
}

func (l *ledger) fund(user, asset string, amount decimal.Decimal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.spot[user] == nil {
		l.spot[user] = map[string]decimal.Decimal{}
	}
	l.spot[user][asset] = l.spot[user][asset].Add(amount)
}

func digest(p ports.Posting) [32]byte {
	s := fmt.Sprintf("%s|%s|%s|", p.UserID, p.Account.Key(), p.Reference)
	for _, m := range p.Moves {
		s += fmt.Sprintf("%s:%s:%s;", m.Type, m.Asset, m.Amount)
	}
	return sha256.Sum256([]byte(s))
}

func insufficient(what string) error {
	return apperr.New(apperr.KindUnprocessable, "LEDGER_INSUFFICIENT_BALANCE", "insufficient "+what)
}

func (l *ledger) Post(_ context.Context, p ports.Posting) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "ledger down")
	}
	if h, ok := l.done[p.IdemKey]; ok {
		if h != digest(p) {
			return nil, apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict, "key reused")
		}
		return make([]string, len(p.Moves)), nil
	}
	// Work on copies; commit only when every move passed.
	spot := map[string]decimal.Decimal{}
	for a, v := range l.spot[p.UserID] {
		spot[a] = v
	}
	acct := map[string]balances{}
	for a, b := range l.margin[p.UserID][p.Account] {
		acct[a] = *b
	}
	lent, income := map[string]decimal.Decimal{}, map[string]decimal.Decimal{}
	for _, m := range p.Moves {
		b := acct[m.Asset]
		switch m.Type {
		case domain.MoveTransferIn:
			if spot[m.Asset].LessThan(m.Amount) {
				return nil, insufficient("spot")
			}
			spot[m.Asset], b.free = spot[m.Asset].Sub(m.Amount), b.free.Add(m.Amount)
		case domain.MoveTransferOut:
			if b.free.LessThan(m.Amount) || b.free.Add(b.locked).Sub(m.Amount).LessThan(b.borrowed.Add(b.interest)) {
				return nil, insufficient("free")
			}
			spot[m.Asset], b.free = spot[m.Asset].Add(m.Amount), b.free.Sub(m.Amount)
		case domain.MoveBorrow:
			b.free, b.borrowed = b.free.Add(m.Amount), b.borrowed.Add(m.Amount)
			lent[m.Asset] = lent[m.Asset].Add(m.Amount)
		case domain.MoveInterest:
			b.interest = b.interest.Add(m.Amount)
			income[m.Asset] = income[m.Asset].Add(m.Amount)
		case domain.MoveRepay:
			principal := m.Amount.Sub(m.Interest)
			if b.free.LessThan(m.Amount) || b.interest.LessThan(m.Interest) || b.borrowed.LessThan(principal) {
				return nil, insufficient("free, interest or principal")
			}
			b.free, b.interest, b.borrowed = b.free.Sub(m.Amount), b.interest.Sub(m.Interest), b.borrowed.Sub(principal)
			lent[m.Asset] = lent[m.Asset].Sub(principal)
		default:
			return nil, apperr.Invalid("unknown move " + string(m.Type))
		}
		acct[m.Asset] = b
	}
	l.spot[p.UserID] = spot
	if l.margin[p.UserID] == nil {
		l.margin[p.UserID] = map[domain.Account]map[string]*balances{}
	}
	stored := map[string]*balances{}
	for a, b := range acct {
		stored[a] = &b
	}
	l.margin[p.UserID][p.Account] = stored
	for a, v := range lent {
		l.lent[a] = l.lent[a].Add(v)
	}
	for a, v := range income {
		l.income[a] = l.income[a].Add(v)
	}
	l.done[p.IdemKey] = digest(p)
	l.posts++
	journals := make([]string, len(p.Moves))
	for i := range journals {
		journals[i] = uuid.Must(uuid.NewV7()).String()
	}
	return journals, nil
}

func (l *ledger) Accrue(_ context.Context, key, asset, reference string, lines []ports.Accrual) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return "", apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "ledger down")
	}
	text := asset + "|" + reference + "|"
	for _, a := range lines {
		text += fmt.Sprintf("%s:%s:%s;", a.UserID, a.Account.Key(), a.Amount)
	}
	h := sha256.Sum256([]byte(text))
	if prev, ok := l.done["margin-interest:"+key]; ok {
		if prev != h {
			return "", apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict, "key reused")
		}
		return "replayed", nil
	}
	for _, a := range lines {
		b := l.margin[a.UserID][a.Account][asset]
		if b == nil {
			return "", apperr.Invalid("no such loan")
		}
		b.interest = b.interest.Add(a.Amount)
		l.income[asset] = l.income[asset].Add(a.Amount)
	}
	l.done["margin-interest:"+key] = h
	l.posts++
	return uuid.Must(uuid.NewV7()).String(), nil
}

func (l *ledger) Debts(context.Context) ([]ports.Debt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []ports.Debt
	for user, accounts := range l.margin {
		for a, assets := range accounts {
			for asset, b := range assets {
				if b.borrowed.IsPositive() || b.interest.IsPositive() {
					out = append(out, ports.Debt{UserID: user, Account: a, Asset: asset, Principal: b.borrowed, Interest: b.interest})
				}
			}
		}
	}
	return out, nil
}

func (l *ledger) Holdings(_ context.Context, userID string) (map[domain.Account][]domain.Holding, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "ledger down")
	}
	out := map[domain.Account][]domain.Holding{}
	for a, assets := range l.margin[userID] {
		for asset, b := range assets {
			out[a] = append(out[a], domain.Holding{Asset: asset, Free: b.free, Locked: b.locked, Borrowed: b.borrowed, Interest: b.interest})
		}
	}
	return out, nil
}

func (l *ledger) owed(user string, a domain.Account, asset string) balances {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b := l.margin[user][a][asset]; b != nil {
		return *b
	}
	return balances{}
}

type prices struct {
	mu sync.Mutex
	p  domain.Prices
}

func (p *prices) Prices() domain.Prices {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := domain.Prices{}
	for k, v := range p.p {
		out[k] = v
	}
	return out
}

// setBTC moves BTC's price.
func (p *prices) setBTC(value string, fresh bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.p["BTC"] = domain.Price{Value: d(value), Fresh: fresh}
}

type instruments struct{}

var decimalsOf = map[string]int32{"USDT": 6, "BTC": 8, "ETH": 8, "ASTRA": 8}

func (instruments) Decimals(_ context.Context, asset string) (int32, error) {
	if n, ok := decimalsOf[asset]; ok {
		return n, nil
	}
	return 0, apperr.NotFound("no such asset")
}

func (instruments) Pair(_ context.Context, symbol string) (ports.PairInfo, error) {
	base, quote, ok := strings.Cut(symbol, "-")
	if !ok {
		return ports.PairInfo{}, apperr.NotFound("no such pair")
	}
	return ports.PairInfo{Symbol: symbol, Base: base, Quote: quote, Status: "TRADING", TickDecimals: 2}, nil
}

type eligible struct{}

func (eligible) Check(context.Context, string, string, string) (bool, string, error) {
	return true, "", nil
}

type features struct {
	mu sync.Mutex
	on map[string]bool
}

func (f *features) Enabled(key string, _ flags.Subject) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on[key]
}

func (f *features) set(key string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.on[key] = on
}

type rig struct {
	svc      *application.Service
	store    *postgres.Store
	ledger   *ledger
	prices   *prices
	features *features
	clock    time.Time
	mu       sync.Mutex
}

func (r *rig) now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clock
}

func (r *rig) at(t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clock = t
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	db := testenv.Postgres(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, os.DirFS("../../../migrations/margin"), log); err != nil {
		t.Fatal(err)
	}
	r := &rig{
		store: postgres.NewStore(db, event.NewFactory("margin-service", "test")), ledger: newLedger(),
		prices:   &prices{p: domain.Prices{"BTC": {Value: d("30000"), Fresh: true}, "ETH": {Value: d("2000"), Fresh: true}}},
		features: &features{on: map[string]bool{flags.KeyMarginEnabled: true}},
		clock:    time.Date(2026, 10, 6, 10, 15, 0, 0, time.UTC),
	}
	r.svc = &application.Service{
		Store: r.store, Ledger: r.ledger, Prices: r.prices, Instruments: instruments{}, Eligibility: eligible{}, Features: r.features,
		Log: log, Now: r.now, Metrics: application.NewMetrics(prometheus.NewRegistry()),
	}
	seed(t, r.store)
	return r
}

// seed stores design §4's terms for USDT, BTC, ETH and ASTRA (ASTRA not
// borrowable here) and BTC-USDT at 10x.
func seed(t *testing.T, s *postgres.Store) {
	t.Helper()
	ctx := context.Background()
	assets := []domain.AssetTerms{
		{
			Asset: "USDT", Borrowable: true, Collateral: true, Haircut: d("1"), PoolCap: d("2000000"), UserCap: d("200000"),
			Model: domain.InterestFixed, FixedRate: d("0.00001"), Floating: domain.DefaultFloating(),
		},
		{
			Asset: "BTC", Borrowable: true, Collateral: true, Haircut: d("0.95"), PoolCap: d("20"), UserCap: d("2"),
			Model: domain.InterestFloating, FixedRate: d("0.000005"), Floating: domain.DefaultFloating(),
		},
		{
			Asset: "ETH", Borrowable: true, Collateral: true, Haircut: d("0.95"), PoolCap: d("400"), UserCap: d("40"),
			Model: domain.InterestFixed, FixedRate: d("0.000005"), Floating: domain.DefaultFloating(),
		},
		{
			Asset: "ASTRA", Borrowable: false, Collateral: true, Haircut: d("0.7"), PoolCap: d("100000"), UserCap: d("10000"),
			Model: domain.InterestFixed, FixedRate: d("0.00003"), Floating: domain.DefaultFloating(),
		},
	}
	err := s.Tx(ctx, func(r ports.Repos) error {
		for _, a := range assets {
			if err := r.Terms().SaveAsset(ctx, a, "test"); err != nil {
				return err
			}
		}
		if err := r.Terms().SavePair(ctx, domain.Pair{
			Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT", Isolated: true,
			Terms: domain.DefaultTerms(domain.AccountIsolated, 10),
		}, "test"); err != nil {
			return err
		}
		// An operator's warning level above 10/9: the level bounds loans.
		strict := domain.DefaultTerms(domain.AccountIsolated, 10)
		strict.WarnLevel = d("1.15")
		if err := r.Terms().SavePair(ctx, domain.Pair{Symbol: "ETH-USDT", Base: "ETH", Quote: "USDT", Isolated: true, Terms: strict},
			"test"); err != nil {
			return err
		}
		return r.Terms().SaveCross(ctx, domain.DefaultTerms(domain.AccountCross, 3), "test")
	})
	if err != nil {
		t.Fatal(err)
	}
}

func code(err error) string {
	if err == nil {
		return ""
	}
	return apperr.From(err).Code
}

func TestTransferBorrowRepay(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross := domain.Cross()
	r.ledger.fund(user, "USDT", d("5000"))

	// Before any transfer the account has no room.
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-0", Account: cross, Asset: "USDT", Amount: d("1")}); code(err) != "MARGIN_LIMIT" {
		t.Fatalf("borrow before a transfer: %v", err)
	}
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-in", Direction: domain.DirectionIn,
		Account: cross, Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	// ASTRA is collateral but not borrowable; SOL is not on the list.
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-astra", Account: cross, Asset: "ASTRA", Amount: d("1")}); code(err) != "MARGIN_ASSET_NOT_BORROWABLE" {
		t.Fatalf("ASTRA: %v", err)
	}
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-sol", Direction: domain.DirectionIn,
		Account: cross, Asset: "SOL", Amount: d("1"),
	}); code(err) != "MARGIN_ASSET_NOT_BORROWABLE" {
		t.Fatalf("SOL: %v", err)
	}

	room, err := r.svc.MaxBorrowable(ctx, user, cross, "USDT")
	if err != nil || !room.Amount.Equal(d("2000")) || room.LimitedBy != domain.LimitLeverage {
		t.Fatalf("max borrowable %+v %v", room, err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-big", Account: cross, Asset: "USDT", Amount: d("2000.000001")}); code(err) != "MARGIN_LIMIT" {
		t.Fatalf("beyond the leverage: %v", err)
	}
	in := application.BorrowInput{UserID: user, IdemKey: "b-1", Account: cross, Asset: "USDT", Amount: d("1500")}
	loan, err := r.svc.Borrow(ctx, in)
	// The first hour at 0.0010%: 1500 x 0.00001 = 0.015.
	if err != nil || !loan.Principal.Equal(d("1500")) || !loan.Interest.Equal(d("0.015")) {
		t.Fatalf("borrow: %+v %v", loan, err)
	}
	if b := r.ledger.owed(user, cross, "USDT"); !b.free.Equal(d("2500")) || !b.borrowed.Equal(d("1500")) || !b.interest.Equal(d("0.015")) {
		t.Fatalf("ledger after the borrow %+v", b)
	}
	// The same key replays; with another amount it conflicts.
	if again, err := r.svc.Borrow(ctx, in); err != nil || !again.Principal.Equal(d("1500")) {
		t.Fatalf("replay: %+v %v", again, err)
	}
	other := in
	other.Amount = d("10")
	if _, err := r.svc.Borrow(ctx, other); code(err) != apperr.CodeIdempotencyConflict {
		t.Fatalf("reused key: %v", err)
	}
	posts := r.ledger.posts

	// Out: 1000 own, 1500 borrowed and 0.015 owed; at most what keeps
	// 2500 - x >= 1.3 x 1500.015, and never the borrowed USDT.
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-out-big", Direction: domain.DirectionOut,
		Account: cross, Asset: "USDT", Amount: d("600"),
	}); code(err) != "MARGIN_LEVEL_TOO_LOW" {
		t.Fatalf("out beyond the level: %v", err)
	}
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-out", Direction: domain.DirectionOut,
		Account: cross, Asset: "USDT", Amount: d("500"),
	}); err != nil {
		t.Fatalf("out: %v", err)
	}

	// Repay 10: the interest first.
	res, err := r.svc.Repay(ctx, application.RepayInput{UserID: user, IdemKey: "r-1", Account: cross, Asset: "USDT", Amount: d("10")})
	if err != nil || !res.Repay.Interest.Equal(d("0.015")) || !res.Repay.Principal.Equal(d("9.985")) || !res.Loan.Principal.Equal(d("1490.015")) ||
		!res.Loan.Interest.IsZero() {
		t.Fatalf("repay 10: %+v %v", res, err)
	}
	// Margin trading switched off: borrowing and transfers in stop,
	// repaying does not.
	r.features.set(flags.KeyMarginEnabled, false)
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-off", Account: cross, Asset: "USDT", Amount: d("1")}); code(err) != "MARGIN_DISABLED" {
		t.Fatalf("borrow while off: %v", err)
	}
	res, err = r.svc.Repay(ctx, application.RepayInput{UserID: user, IdemKey: "r-all", Account: cross, Asset: "USDT", All: true})
	if err != nil || !res.Repay.Principal.Equal(d("1490.015")) || !res.Loan.Principal.IsZero() {
		t.Fatalf("repay all: %+v %v", res, err)
	}
	// 1000 in, 500 out, 0.015 of interest paid.
	if b := r.ledger.owed(user, cross, "USDT"); !b.free.Equal(d("499.985")) || !b.borrowed.IsZero() || !b.interest.IsZero() {
		t.Fatalf("ledger after repaying %+v", b)
	}
	if !r.ledger.lent["USDT"].IsZero() || !r.ledger.income["USDT"].Equal(d("0.015")) {
		t.Fatalf("HOUSE: lent %s, income %s", r.ledger.lent["USDT"], r.ledger.income["USDT"])
	}
	if r.ledger.posts <= posts {
		t.Fatal("no postings after the replay")
	}
	lent, err := r.store.Read().Pools().Lent(ctx)
	if err != nil || !lent["USDT"].IsZero() {
		t.Fatalf("pool %v %v", lent, err)
	}
}

func TestPoolAndUserCaps(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	cross := domain.Cross()
	rich := func() string {
		u := uuid.Must(uuid.NewV7()).String()
		r.ledger.fund(u, "USDT", d("10000000"))
		if _, err := r.svc.Transfer(ctx, application.TransferInput{
			UserID: u, IdemKey: "t", Direction: domain.DirectionIn, Account: cross,
			Asset: "USDT", Amount: d("10000000"),
		}); err != nil {
			t.Fatal(err)
		}
		return u
	}
	a, b := rich(), rich()
	// The user's cap: 2 BTC.
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: a, IdemKey: "b1", Account: cross, Asset: "BTC", Amount: d("2.1")}); code(err) != "MARGIN_LIMIT" {
		t.Fatalf("over the user's cap: %v", err)
	}
	for i, u := range []string{a, b} {
		if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: u, IdemKey: "b2", Account: cross, Asset: "BTC", Amount: d("2")}); err != nil {
			t.Fatalf("borrow %d: %v", i, err)
		}
	}
	// The pool: 20 BTC, 4 lent; shrink the cap under what is lent.
	err := r.store.Tx(ctx, func(rp ports.Repos) error {
		t, _, err := rp.Terms().Asset(ctx, "BTC")
		if err != nil {
			return err
		}
		t.PoolCap, t.UserCap = d("4.5"), d("2")
		return rp.Terms().SaveAsset(ctx, t, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	c := rich()
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: c, IdemKey: "b3", Account: cross, Asset: "BTC", Amount: d("1")}); code(err) != "MARGIN_POOL_EMPTY" {
		t.Fatalf("over the pool: %v", err)
	}
	if m, err := r.svc.MaxBorrowable(ctx, c, cross, "BTC"); err != nil || !m.Amount.Equal(d("0.5")) || m.LimitedBy != domain.LimitPool {
		t.Fatalf("max %+v %v", m, err)
	}
}

func TestIsolatedAccount(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	iso := domain.Isolated("BTC-USDT")
	r.ledger.fund(user, "USDT", d("1000"))
	r.ledger.fund(user, "ETH", d("1"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-eth", Direction: domain.DirectionIn, Account: iso,
		Asset: "ETH", Amount: d("1"),
	}); code(err) != "MARGIN_ASSET_NOT_BORROWABLE" {
		t.Fatalf("ETH into BTC-USDT: %v", err)
	}
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-in", Direction: domain.DirectionIn, Account: iso,
		Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	// 10x, warned at 1.10: nine times the net assets.
	m, err := r.svc.MaxBorrowable(ctx, user, iso, "USDT")
	if err != nil || !m.Amount.Equal(d("9000")) || m.LimitedBy != domain.LimitLeverage {
		t.Fatalf("max %+v %v", m, err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-big", Account: iso, Asset: "USDT", Amount: d("9000.000001")}); code(err) != "MARGIN_LIMIT" {
		t.Fatalf("over the leverage: %v", err)
	}
	// ETH-USDT warns at 1.15: the level bound decides there.
	eth := domain.Isolated("ETH-USDT")
	r.ledger.fund(user, "USDT", d("1000"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-eth-in", Direction: domain.DirectionIn, Account: eth,
		Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	if m, err := r.svc.MaxBorrowable(ctx, user, eth, "USDT"); err != nil || !m.Amount.Equal(d("6666.666666")) || m.LimitedBy != domain.LimitLevel {
		t.Fatalf("max on ETH-USDT %+v %v", m, err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-eth", Account: eth, Asset: "USDT", Amount: d("7000")}); code(err) != "MARGIN_LEVEL_TOO_LOW" {
		t.Fatalf("under the warning level: %v", err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b", Account: iso, Asset: "USDT", Amount: d("3000")}); err != nil {
		t.Fatal(err)
	}
	cross, isolated, err := r.svc.Accounts(ctx, user)
	if err != nil || len(isolated) != 2 || !cross.Valuation.TotalAsset.IsZero() || isolated[0].Account != iso {
		t.Fatalf("accounts %+v %+v %v", cross, isolated, err)
	}
	v := isolated[0]
	if level, ok := v.Valuation.Level(); !ok || v.Terms.Leverage != 10 || !v.Valuation.TotalAsset.Equal(d("4000")) || !level.Equal(d("1.33332")) {
		t.Fatalf("isolated %+v level %s", v, level)
	}
	// A stale price counts at its last value; an asset never priced stops
	// what adds risk.
	r.prices.setBTC("30000", false)
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-stale", Account: iso, Asset: "BTC", Amount: d("0.001")}); err != nil {
		t.Fatalf("stale price: %v", err)
	}
	r.prices.mu.Lock()
	delete(r.prices.p, "BTC")
	r.prices.mu.Unlock()
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-unpriced", Account: iso, Asset: "USDT", Amount: d("1")}); code(err) != "MARGIN_PRICE_UNAVAILABLE" {
		t.Fatalf("never priced: %v", err)
	}
}

func TestLedgerOutageAndRecovery(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross := domain.Cross()
	r.ledger.fund(user, "USDT", d("1000"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t", Direction: domain.DirectionIn, Account: cross,
		Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	// The ledger answers the holdings but fails the posting.
	in := application.BorrowInput{UserID: user, IdemKey: "b", Account: cross, Asset: "USDT", Amount: d("100")}
	r.svc.Ledger = &failingLedger{ledger: r.ledger}
	if _, err := r.svc.Borrow(ctx, in); code(err) != apperr.CodeUnavailable {
		t.Fatalf("borrow during the outage: %v", err)
	}
	lent, _ := r.store.Read().Pools().Lent(ctx)
	if !lent["USDT"].Equal(d("100")) {
		t.Fatalf("the pending borrow reserves the pool: %v", lent)
	}
	// Back: recovery books it once, a retry reports it.
	r.svc.Ledger = r.ledger
	r.at(r.now().Add(time.Minute))
	if n, err := r.svc.Recover(ctx); err != nil || n != 1 {
		t.Fatalf("recover %d %v", n, err)
	}
	loan, err := r.svc.Borrow(ctx, in)
	if err != nil || !loan.Principal.Equal(d("100")) {
		t.Fatalf("retry: %+v %v", loan, err)
	}
	if b := r.ledger.owed(user, cross, "USDT"); !b.borrowed.Equal(d("100")) {
		t.Fatalf("booked %+v", b)
	}
}

// failingLedger answers the holdings and fails every posting.
type failingLedger struct{ *ledger }

func (f *failingLedger) Post(context.Context, ports.Posting) ([]string, error) {
	return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "ledger down")
}

func TestHourlyInterest(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross := domain.Cross()
	r.ledger.fund(user, "USDT", d("100000"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t", Direction: domain.DirectionIn, Account: cross,
		Asset: "USDT", Amount: d("100000"),
	}); err != nil {
		t.Fatal(err)
	}
	// 10:15 borrow 1000 USDT (first hour now) and 1 BTC (floating: 1 of
	// 20 lent, 5% use).
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-usdt", Account: cross, Asset: "USDT", Amount: d("1000")}); err != nil {
		t.Fatal(err)
	}
	btc, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-btc", Account: cross, Asset: "BTC", Amount: d("1")})
	if err != nil {
		t.Fatal(err)
	}
	// The hour of 10:00 had its rate set from the pool before the loan (0
	// lent): the base rate, 0.000005 x 1 BTC.
	if !btc.Interest.Equal(d("0.000005")) {
		t.Fatalf("BTC first hour %s", btc.Interest)
	}
	// The first run (10:15) starts with its own hour: both loans were made
	// after 10:00, so nothing is owed on it.
	if n, err := r.svc.ChargeInterest(ctx); err != nil || n != 0 {
		t.Fatalf("10:00 run: %d %v", n, err)
	}
	// 11:00:35 the next run (an hour waits 30 seconds); 11:00:02 another
	// 500 USDT was borrowed (paid its first hour), so 11:00 charges 1000
	// USDT and 1 BTC.
	r.at(time.Date(2026, 10, 6, 11, 0, 2, 0, time.UTC))
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-usdt-2", Account: cross, Asset: "USDT", Amount: d("500")}); err != nil {
		t.Fatal(err)
	}
	r.at(time.Date(2026, 10, 6, 11, 0, 35, 0, time.UTC))
	if n, err := r.svc.ChargeInterest(ctx); err != nil || n != 2 {
		t.Fatalf("11:00 run: %d %v", n, err)
	}
	// USDT: 0.01 + 0.005 at borrowing, 0.01 on the hour.
	if b := r.ledger.owed(user, cross, "USDT"); !b.interest.Equal(d("0.025")) || !b.borrowed.Equal(d("1500")) {
		t.Fatalf("USDT %+v", b)
	}
	// BTC at 11:00: 1 of 20 lent, 5% use: 0.000005 + 0.000025 x 0.05/0.8.
	if b := r.ledger.owed(user, cross, "BTC"); !b.interest.Equal(d("0.000005").Add(d("0.00000657"))) {
		t.Fatalf("BTC %+v", b)
	}
	// A second pass of the same hour charges nothing.
	if n, err := r.svc.ChargeInterest(ctx); err != nil || n != 0 {
		t.Fatalf("again: %d %v", n, err)
	}
	// Down from 11:30 to 14:20: 12:00, 13:00 and 14:00 are charged on
	// 1500 (the 300 repaid at 13:30 still owed at 13:00, not at 14:00).
	r.at(time.Date(2026, 10, 6, 13, 30, 0, 0, time.UTC))
	if l, err := r.store.Read().Loans().Get(ctx, user, cross, "USDT"); err != nil || !l.Interest.Equal(d("0.025")) || !l.Principal.Equal(d("1500")) {
		t.Fatalf("the loan book before repaying: %+v %v", l, err)
	}
	if _, err := r.svc.Repay(ctx, application.RepayInput{UserID: user, IdemKey: "r", Account: cross, Asset: "USDT", Amount: d("300.025")}); err != nil {
		t.Fatal(err)
	}
	r.at(time.Date(2026, 10, 6, 14, 20, 0, 0, time.UTC))
	if n, err := r.svc.ChargeInterest(ctx); err != nil || n != 6 {
		t.Fatalf("catch-up: %d %v", n, err)
	}
	// 12:00 and 13:00 on 1500 (0.015 each), 14:00 on 1200 (0.012).
	if b := r.ledger.owed(user, cross, "USDT"); !b.interest.Equal(d("0.042")) || !b.borrowed.Equal(d("1200")) {
		t.Fatalf("USDT after the catch-up %+v", b)
	}
	list, next, err := r.svc.Interest(ctx, user, &cross, "USDT", "", 2)
	if err != nil || len(list) != 2 || next == "" || !list[0].Hour.Equal(time.Date(2026, 10, 6, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("history %+v %q %v", list, next, err)
	}
}

func TestOrdersOnMarginAccounts(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross := domain.Cross()
	r.ledger.fund(user, "USDT", d("2000"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t", Direction: domain.DirectionIn, Account: cross,
		Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	buy := application.OrderInput{
		UserID: user, OrderID: uuid.Must(uuid.NewV7()).String(), Account: cross, Symbol: "BTC-USDT", Side: application.SideBuy,
		FreezeAsset: "USDT", FreezeAmount: d("1500"), SideEffect: domain.SideEffectNone, Price: d("30000"), Quantity: d("0.05"),
	}
	if _, err := r.svc.CheckOrder(ctx, buy); code(err) != "LEDGER_INSUFFICIENT_BALANCE" {
		t.Fatalf("without AUTO_BORROW: %v", err)
	}
	buy.SideEffect = domain.SideEffectAutoBorrow
	if _, err := r.svc.ReserveOrder(ctx, buy); code(err) != "MARGIN_DISABLED" || apperr.From(err).Details["flag"] != flags.KeyMarginAutoBorrow {
		t.Fatalf("AUTO_BORROW switched off: %v", err)
	}
	r.features.set(flags.KeyMarginAutoBorrow, true)
	check, err := r.svc.CheckOrder(ctx, buy)
	// 500 to borrow; after the fill 0.05 BTC x 30000 x 0.95 against 500.
	if err != nil || !check.Borrow.Equal(d("500")) || check.MarginLevel == nil || !check.MarginLevel.Equal(d("2.85")) {
		t.Fatalf("check %+v %v", check, err)
	}
	first, err := r.svc.ReserveOrder(ctx, buy)
	if err != nil || !first.Borrow.Equal(d("500")) || first.BorrowID == "" {
		t.Fatalf("reserve %+v %v", first, err)
	}
	again, err := r.svc.ReserveOrder(ctx, buy)
	if err != nil || again.BorrowID != first.BorrowID || !again.Borrow.Equal(d("500")) {
		t.Fatalf("a repeat %+v %v", again, err)
	}
	if b := r.ledger.owed(user, cross, "USDT"); !b.borrowed.Equal(d("500")) {
		t.Fatalf("borrowed once: %+v", b)
	}
	// An order the free balance covers borrows nothing, and says so again.
	small := buy
	small.OrderID, small.FreezeAmount, small.Quantity = uuid.Must(uuid.NewV7()).String(), d("30"), d("0.001")
	if out, err := r.svc.ReserveOrder(ctx, small); err != nil || !out.Borrow.IsZero() || out.BorrowID != "" {
		t.Fatalf("no borrow %+v %v", out, err)
	}
	if out, err := r.svc.ReserveOrder(ctx, small); err != nil || !out.Borrow.IsZero() {
		t.Fatalf("no borrow again %+v %v", out, err)
	}
	// ETH-USDT warns at 1.15: a loan past it is refused.
	eth := domain.Isolated("ETH-USDT")
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "t-eth", Direction: domain.DirectionIn, Account: eth,
		Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	big := application.OrderInput{
		UserID: user, OrderID: uuid.Must(uuid.NewV7()).String(), Account: eth, Symbol: "ETH-USDT", Side: application.SideBuy,
		FreezeAsset: "USDT", FreezeAmount: d("8000"), SideEffect: domain.SideEffectAutoBorrow, Price: d("2000"), Quantity: d("4"),
	}
	if _, err := r.svc.ReserveOrder(ctx, big); code(err) != "MARGIN_LEVEL_TOO_LOW" {
		t.Fatalf("past the warning level: %v", err)
	}
	wrong := big
	wrong.Symbol = "BTC-USDT"
	if _, err := r.svc.CheckOrder(ctx, wrong); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an order of another pair on ETH-USDT's account: %v", err)
	}
	frozen := big
	frozen.FreezeAsset = "ETH"
	if _, err := r.svc.CheckOrder(ctx, frozen); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a buy freezing the base: %v", err)
	}

	// The settlement repaid 100 of the loan automatically (ledger.events).
	entry := application.Entry{
		JournalID: uuid.Must(uuid.NewV7()).String(), EntryType: "MARGIN_REPAY", IdemKey: "trade-repay:" + uuid.NewString() + ":seller",
		Memo: "auto-repay order " + buy.OrderID + " trade BTC-USDT x", At: r.now(),
		Lines: []application.EntryLine{
			{UserID: user, AccountType: "MARGIN_CROSS", Asset: "USDT", Amount: d("-100.005")},
			{UserID: user, AccountType: "MARGIN_CROSS_INTEREST", Asset: "USDT", Amount: d("0.005")},
			{UserID: user, AccountType: "MARGIN_CROSS_DEBT", Asset: "USDT", Amount: d("100")},
		},
	}
	for range 2 { // a redelivery changes nothing
		if err := r.svc.OnEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	loan, err := r.store.Read().Loans().Get(ctx, user, cross, "USDT")
	if err != nil || !loan.Principal.Equal(d("400")) || !loan.Interest.IsZero() {
		t.Fatalf("the loan after the automatic repayment %+v %v", loan, err)
	}
	lent, err := r.store.Read().Pools().Lent(ctx)
	if err != nil || !lent["USDT"].Equal(d("400")) {
		t.Fatalf("the pool %v %v", lent, err)
	}
	repay, ok, err := r.store.Read().Repays().ByKey(ctx, user, entry.IdemKey)
	if err != nil || !ok || repay.Reason != ports.RepayAuto || repay.OrderID != buy.OrderID {
		t.Fatalf("recorded %+v %v %v", repay, ok, err)
	}
}
