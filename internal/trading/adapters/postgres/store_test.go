package postgres_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/internal/trading/adapters/postgres"
	"github.com/skill/exchange/internal/trading/adapters/prices"
	"github.com/skill/exchange/internal/trading/application"
	"github.com/skill/exchange/internal/trading/domain"
	"github.com/skill/exchange/internal/trading/ports"
	"github.com/skill/exchange/migrations"
)

func setup(t *testing.T) (*postgres.Store, *pg.DB) {
	t.Helper()
	db := testenv.Postgres(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Trading(), log); err != nil {
		t.Fatal(err)
	}
	return postgres.NewStore(db, event.NewFactory("spot-trading-service", "test")), db
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var pair = domain.Pair{
	Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT", TickSize: d("0.01"), LotSize: d("0.00001"),
	MinQuantity: d("0.00001"), MaxQuantity: d("100"), MinNotional: d("5"), PriceBand: d("0.1"),
	MakerFeeRate: d("0.001"), TakerFeeRate: d("0.002"), Status: domain.PairTrading, BaseDecimals: 8, QuoteDecimals: 6, Tradable: true,
}

func order(t *testing.T, user string, req domain.Request, at time.Time) domain.Order {
	t.Helper()
	req.UserID = user
	if req.Symbol == "" {
		req.Symbol = pair.Symbol
	}
	o, err := domain.NewOrder(uuid.Must(uuid.NewV7()).String(), req, pair, d("60000"), at)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func limitBuy() domain.Request {
	return domain.Request{Side: domain.SideBuy, Type: domain.TypeLimit, Price: d("60000.01"), Quantity: d("0.00013")}
}

func TestOrdersRoundTrip(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	o := order(t, uuid.NewString(), limitBuy(), at)
	market := order(t, o.UserID, domain.Request{Side: domain.SideBuy, Type: domain.TypeMarket, QuoteAmount: d("100")}, at)
	for _, x := range []domain.Order{o, market} {
		if err := store.Read().Orders().Insert(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Read().Orders().Get(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Price.Equal(d("60000.01")) || !got.Quantity.Equal(d("0.00013")) || !got.QuoteAmount.IsZero() ||
		!got.FrozenAmount.Equal(d("7.800002")) || got.FreezeState != domain.FreezePending || got.TimeInForce != domain.GTC ||
		!got.TakerFeeRate.Equal(d("0.002")) || got.QuoteDecimals != 6 || !got.CreatedAt.Equal(at) || got.ClientOrderID != o.ID {
		t.Fatalf("round trip: %+v", got)
	}
	if got.AccountType != domain.AccountSpot || got.SideEffect != domain.SideEffectNone {
		t.Fatalf("a spot order's account: %q %q", got.AccountType, got.SideEffect)
	}
	m, _ := store.Read().Orders().Get(ctx, market.ID)
	if !m.QuoteAmount.Equal(d("100")) || !m.Price.IsZero() || !m.ProtectionPrice.Equal(d("66000")) {
		t.Fatalf("market order: %+v", m)
	}
	if byClient, err := store.Read().Orders().ByClientID(ctx, o.UserID, o.ClientOrderID); err != nil || byClient.ID != o.ID {
		t.Fatalf("by client id: %v %v", byClient.ID, err)
	}
	got.Status, got.RejectReason, got.FreezeState, got.CancelRequested, got.Sequence =
		domain.StatusRejected, "LEDGER_INSUFFICIENT_BALANCE", domain.FreezeNone, true, 7
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Orders().Update(ctx, got) }); err != nil {
		t.Fatal(err)
	}
	again, _ := store.Read().Orders().Get(ctx, o.ID)
	if again.Status != domain.StatusRejected || again.RejectReason != "LEDGER_INSUFFICIENT_BALANCE" || !again.CancelRequested || again.Sequence != 7 {
		t.Fatalf("update: %+v", again)
	}
	if _, err := store.Read().Orders().Get(ctx, uuid.NewString()); err != domain.ErrOrderNotFound { //nolint:errorlint // sentinel returned as is
		t.Fatalf("missing order: %v", err)
	}
}

func TestMarginOrdersKeepTheirAccount(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	req := limitBuy()
	req.AccountType, req.SideEffect = domain.AccountMarginIsolated, domain.SideEffectAutoBorrow
	o := order(t, uuid.NewString(), req, time.Now())
	if err := store.Read().Orders().Insert(ctx, o); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read().Orders().Get(ctx, o.ID)
	if err != nil || got.AccountType != domain.AccountMarginIsolated || got.SideEffect != domain.SideEffectAutoBorrow ||
		got.Account() != (domain.Account{UserID: o.UserID, Type: domain.AccountMarginIsolated, Scope: "BTC-USDT"}) ||
		!got.Borrowed.IsZero() || got.BorrowID != "" {
		t.Fatalf("margin order: %+v %v", got, err)
	}
	// What margin-service borrowed for it is recorded with its freeze.
	borrowID := uuid.Must(uuid.NewV7()).String()
	got.FreezeState, got.Borrowed, got.BorrowID = domain.FreezeDone, d("7.8"), borrowID
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Orders().Update(ctx, got) }); err != nil {
		t.Fatal(err)
	}
	if again, _ := store.Read().Orders().Get(ctx, o.ID); !again.Borrowed.Equal(d("7.8")) || again.BorrowID != borrowID {
		t.Fatalf("borrow on record: %s %q", again.Borrowed, again.BorrowID)
	}
	if _, err := db.Exec(ctx, `UPDATE orders SET borrow_id = NULL WHERE id = $1`, o.ID); err == nil {
		t.Fatal("a borrow lost its ID")
	}
	// A refusal keeps its status and details, numbers exact.
	got.Status, got.RejectReason, got.RejectStatus = domain.StatusRejected, "MARGIN_LIMIT", 422
	got.RejectDetails = map[string]any{"max_borrowable": "12.5", "count": json.Number("9007199254740993")}
	if err := store.Tx(ctx, func(r ports.Repos) error { return r.Orders().Update(ctx, got) }); err != nil {
		t.Fatal(err)
	}
	refused, _ := store.Read().Orders().Get(ctx, o.ID)
	if refused.RejectStatus != 422 || refused.RejectDetails["max_borrowable"] != "12.5" || refused.RejectDetails["count"] != json.Number("9007199254740993") {
		t.Fatalf("refusal on record: %d %#v", refused.RejectStatus, refused.RejectDetails)
	}
	// A liquidation order names its liquidation; only margin market orders may.
	liq := order(t, o.UserID, domain.Request{
		Side: domain.SideSell, Type: domain.TypeMarket, Quantity: d("0.00002"), AccountType: domain.AccountMarginCross,
		LiquidationID: uuid.Must(uuid.NewV7()).String(),
	}, time.Now())
	if err := store.Read().Orders().Insert(ctx, liq); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Read().Orders().Get(ctx, liq.ID); got.LiquidationID != liq.LiquidationID || !got.ProtectionPrice.Equal(liq.ProtectionPrice) {
		t.Fatalf("liquidation order: %q %s, want %q %s", got.LiquidationID, got.ProtectionPrice, liq.LiquidationID, liq.ProtectionPrice)
	}
	if _, err := db.Exec(ctx, `UPDATE orders SET account_type = 'SPOT', side_effect = 'NONE' WHERE id = $1`, liq.ID); err == nil {
		t.Fatal("a SPOT order named a liquidation")
	}
	// An order built without them is stored as a SPOT order without a
	// side effect; the table refuses a side effect on SPOT.
	bare := order(t, o.UserID, limitBuy(), time.Now())
	bare.AccountType, bare.SideEffect = "", ""
	if err := store.Read().Orders().Insert(ctx, bare); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Read().Orders().Get(ctx, bare.ID); got.AccountType != domain.AccountSpot || got.SideEffect != domain.SideEffectNone {
		t.Fatalf("bare order: %q %q", got.AccountType, got.SideEffect)
	}
	if _, err := db.Exec(ctx, `UPDATE orders SET side_effect = 'AUTO_REPAY' WHERE id = $1`, bare.ID); err == nil {
		t.Fatal("a SPOT order took a side effect")
	}
}

func TestActiveOrdersAndPages(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	user, at := uuid.NewString(), time.Now().Add(-time.Minute)
	var ids []string
	for i, status := range []domain.Status{domain.StatusNew, domain.StatusOpen, domain.StatusFilled, domain.StatusPartiallyFilled} {
		o := order(t, user, limitBuy(), at.Add(time.Duration(i)*time.Second))
		o.Status = status
		if status != domain.StatusNew {
			o.FreezeState = domain.FreezeDone
		}
		if err := store.Read().Orders().Insert(ctx, o); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, o.ID)
	}
	other := order(t, user, domain.Request{Symbol: "BTC-USDT", Side: domain.SideSell, Type: domain.TypeLimit, Price: d("61000"), Quantity: d("0.001")}, at)
	other.Symbol = "ETH-USDT"
	other.FreezeState = domain.FreezeDone
	if err := store.Read().Orders().Insert(ctx, other); err != nil {
		t.Fatal(err)
	}
	onSymbol, total, err := store.Read().Orders().CountActive(ctx, user, "BTC-USDT")
	if err != nil || onSymbol != 3 || total != 4 {
		t.Fatalf("counts: %d %d %v", onSymbol, total, err)
	}
	done := order(t, uuid.NewString(), limitBuy(), at)
	done.Status, done.FreezeState = domain.StatusFilled, domain.FreezeDone
	if err := store.Read().Orders().Insert(ctx, done); err != nil {
		t.Fatal(err)
	}
	users, err := store.Read().Orders().ActiveUsers(ctx)
	if err != nil || !slices.Contains(users, user) || slices.Contains(users, done.UserID) {
		t.Fatalf("users with active orders: %v %v", users, err)
	}
	var active []domain.Order
	err = store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Orders().LockUser(ctx, user); err != nil {
			return err
		}
		var err error
		active, err = r.Orders().Active(ctx, user, "BTC-USDT")
		return err
	})
	if err != nil || len(active) != 3 || active[0].ID != ids[0] {
		t.Fatalf("active: %d %v", len(active), err)
	}
	page, err := store.Read().Orders().List(ctx, user, ports.ListFilter{Symbol: "BTC-USDT", Statuses: domain.ActiveStatuses, Limit: 2})
	if err != nil || len(page) != 2 || page[0].ID != ids[3] || page[1].ID != ids[1] {
		t.Fatalf("page 1: %v", err)
	}
	page, err = store.Read().Orders().List(ctx, user, ports.ListFilter{Symbol: "BTC-USDT", Statuses: domain.ActiveStatuses, Before: page[1].ID, Limit: 2})
	if err != nil || len(page) != 1 || page[0].ID != ids[0] {
		t.Fatalf("page 2: %v", err)
	}
	all, _ := store.Read().Orders().List(ctx, user, ports.ListFilter{Limit: 10})
	if len(all) != 5 {
		t.Fatalf("all orders: %d", len(all))
	}
	pending, err := store.Read().Orders().PendingFreeze(ctx, time.Now(), 10)
	if err != nil || len(pending) != 1 || pending[0].ID != ids[0] {
		t.Fatalf("pending freeze: %d %v", len(pending), err)
	}
	n, oldest, err := store.Read().Orders().PendingStats(ctx)
	if err != nil || n != 1 || oldest.Sub(at).Abs() > time.Millisecond {
		t.Fatalf("pending stats: %d %v %v", n, oldest, err)
	}
}

type freezeOK struct{}

func (freezeOK) Freeze(context.Context, string, domain.Account, string, decimal.Decimal, string) error {
	return nil
}

func (freezeOK) Unfreeze(context.Context, string, domain.Account, string, decimal.Decimal, string) error {
	return nil
}

type onePair struct{}

func (onePair) Pair(context.Context, string) (domain.Pair, error) { return pair, nil }

type eligible struct{}

func (eligible) Check(context.Context, string, string, string) (bool, string, error) {
	return true, "", nil
}

func TestPlacedOrdersQueueTheirEventsAndCommand(t *testing.T) {
	store, db := setup(t)
	ctx := context.Background()
	svc := &application.Service{
		Store: store, Ledger: freezeOK{}, Instruments: onePair{}, Eligibility: eligible{}, Prices: prices.None{},
		Log: slog.New(slog.DiscardHandler), Now: time.Now,
	}
	o, err := svc.Place(ctx, domain.Request{
		UserID: uuid.NewString(), Symbol: "BTC-USDT", Side: domain.SideSell,
		Type: domain.TypeLimit, Price: d("61000"), Quantity: d("0.001"),
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT topic, partition_key, event_type FROM outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var topic, key, typ string
		if err := rows.Scan(&topic, &key, &typ); err != nil {
			t.Fatal(err)
		}
		got = append(got, topic+" "+key+" "+typ)
	}
	want := []string{"order.events BTC-USDT order.OrderAccepted", "order.commands BTC-USDT order.PlaceOrder"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("outbox %v, want %v", got, want)
	}
	if stored, _ := store.Read().Orders().Get(ctx, o.ID); stored.FreezeState != domain.FreezeDone {
		t.Fatalf("stored: %+v", stored)
	}
}

func TestEngineColumnsFillsAndReleases(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	user, at := uuid.NewString(), time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	o := order(t, user, limitBuy(), at)
	o.FreezeState = domain.FreezeDone
	if err := store.Read().Orders().Insert(ctx, o); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Read().Orders().Get(ctx, o.ID)
	if !got.TickSize.Equal(d("0.01")) || !got.LotSize.Equal(d("0.00001")) || got.BaseAsset != "BTC" || got.QuoteAsset != "USDT" || got.Released {
		t.Fatalf("steps and assets: %+v", got)
	}
	if !got.Apply(domain.Update{OrderID: o.ID, Seq: 3, Status: domain.StatusCanceled, Filled: d("0.00005"), FilledQuote: d("3.0000005"), Reason: "IOC"}, at) {
		t.Fatal("apply")
	}
	if err := store.Read().Orders().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	unreleased, err := store.Read().Orders().Unreleased(ctx, time.Now(), 10)
	if err != nil || len(unreleased) != 1 || unreleased[0].CancelReason != "IOC" || !unreleased[0].FilledQuote.Equal(d("3.0000005")) {
		t.Fatalf("unreleased: %+v %v", unreleased, err)
	}
	got.Released = true
	if err := store.Read().Orders().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if unreleased, _ := store.Read().Orders().Unreleased(ctx, time.Now(), 10); len(unreleased) != 0 {
		t.Fatal("a released order is done")
	}

	trade := uuid.NewString()
	fill := domain.Fill{
		TradeID: trade, OrderID: o.ID, UserID: user, Symbol: "BTC-USDT", Side: domain.SideBuy, Maker: true,
		Price: d("60000.01"), Quantity: d("0.00005"), Quote: d("3.0000005"), FeeAsset: "BTC", Fee: d("0.00000005"), Seq: 2, ExecutedAt: at,
	}
	for range 2 {
		if err := store.Read().Fills().Insert(ctx, fill); err != nil {
			t.Fatal(err)
		}
	}
	later := fill
	later.TradeID, later.Seq, later.ExecutedAt = uuid.NewString(), 5, at.Add(time.Second)
	if err := store.Read().Fills().Insert(ctx, later); err != nil {
		t.Fatal(err)
	}
	ofOrder, err := store.Read().Fills().OfOrder(ctx, o.ID)
	if err != nil || len(ofOrder) != 2 || ofOrder[0].TradeID != trade || !ofOrder[0].Maker || !ofOrder[0].Fee.Equal(d("0.00000005")) {
		t.Fatalf("fills of the order: %+v %v", ofOrder, err)
	}
	page, err := store.Read().Fills().OfUser(ctx, user, "BTC-USDT", "", 1)
	if err != nil || len(page) != 1 || page[0].TradeID != later.TradeID {
		t.Fatalf("user page 1: %+v %v", page, err)
	}
	page, err = store.Read().Fills().OfUser(ctx, user, "", page[0].TradeID, 5)
	if err != nil || len(page) != 1 || page[0].TradeID != trade {
		t.Fatalf("user page 2: %+v %v", page, err)
	}
	// The latest trade (by sequence) anchors the price band.
	later.TradeID, later.Seq, later.Price = uuid.NewString(), 4, d("59000")
	if err := store.Read().Fills().Insert(ctx, later); err != nil {
		t.Fatal(err)
	}
	if last, when, err := store.Read().Fills().LastTrade(ctx, "BTC-USDT"); err != nil || !last.Equal(d("60000.01")) || !when.Equal(at.Add(time.Second)) {
		t.Fatalf("last trade %s at %s, %v", last, when, err)
	}
	if none, when, err := store.Read().Fills().LastTrade(ctx, "ETH-USDT"); err != nil || !none.IsZero() || !when.IsZero() {
		t.Fatalf("a symbol without trades: %s, %v", none, err)
	}
}
