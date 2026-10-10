package application_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/application"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// A purge settles a test account's margin accounts (L4b): the orders of
// each account canceled (here an isolated sell of 0.002 BTC, let go of
// once the engine confirms), each debt repaid from what the account holds
// free of the asset, interest first, as AUTO_REPAY under a settle: key;
// what is still owed comes back, and once funds came in a repeat repays
// it. A repeat after that has nothing to do.
func TestSettleRepaysFromWhatTheAccountsHold(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross, iso := domain.Cross(), domain.Isolated("BTC-USDT")
	r.ledger.fund(user, "USDT", d("2000"))
	for i, step := range []func() error{
		func() error {
			_, err := r.svc.Transfer(ctx, application.TransferInput{
				UserID: user, IdemKey: "in-cross", Direction: domain.DirectionIn, Account: cross, Asset: "USDT", Amount: d("1000"),
			})
			return err
		},
		func() error {
			_, err := r.svc.Transfer(ctx, application.TransferInput{
				UserID: user, IdemKey: "in-iso", Direction: domain.DirectionIn, Account: iso, Asset: "USDT", Amount: d("1000"),
			})
			return err
		},
		func() error {
			_, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-usdt", Account: cross, Asset: "USDT", Amount: d("500")})
			return err
		},
		func() error {
			_, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b-btc", Account: iso, Asset: "BTC", Amount: d("0.01")})
			return err
		},
	} {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	btcInterest := r.ledger.owed(user, iso, "BTC").interest
	// 0.004 BTC sold (gone from the account), 0.002 on an open sell.
	r.ledger.mu.Lock()
	b := r.ledger.margin[user][iso]["BTC"]
	b.free, b.locked = d("0.004"), d("0.002")
	r.ledger.mu.Unlock()
	open := 1 // the isolated account's sell
	tr := &trading{ledger: r.ledger, prices: r.prices, cancel: func(_ string, a domain.Account) (int, error) {
		if a != iso {
			return 0, nil
		}
		n := open
		open = 0
		return n, nil
	}}
	r.svc.Trading = tr
	r.svc.Sleep = func(context.Context, time.Duration) error { // the engine confirms the cancel
		r.ledger.mu.Lock()
		defer r.ledger.mu.Unlock()
		b := r.ledger.margin[user][iso]["BTC"]
		b.free, b.locked = b.free.Add(b.locked), decimal.Zero
		return nil
	}
	res, err := r.svc.Settle(ctx, user, "ops@example.com", "purging a test account")
	if err != nil || res.CanceledOrders != 1 || tr.canceled != 2 || len(res.Repaid) != 2 || len(res.Remaining) != 1 || res.Complete() {
		t.Fatalf("settle %+v %v (%d cancels)", res, err, tr.canceled)
	}
	if p := res.Repaid[0]; p.Account != cross || p.Asset != "USDT" || !p.Principal.Equal(d("500")) || !p.Interest.Equal(d("0.005")) {
		t.Fatalf("the cross account's repayment %+v", p)
	}
	if p := res.Repaid[1]; p.Account != iso || p.Asset != "BTC" || !p.Interest.Equal(btcInterest) || !p.Principal.Equal(d("0.006").Sub(btcInterest)) {
		t.Fatalf("the isolated account's repayment %+v (interest %s)", p, btcInterest)
	}
	owed := d("0.004").Add(btcInterest)
	if o := res.Remaining[0]; o.Account != iso || o.Asset != "BTC" || !o.Amount.Equal(owed) {
		t.Fatalf("still owed %+v, want %s", o, owed)
	}
	if b := r.ledger.owed(user, cross, "USDT"); !b.borrowed.IsZero() || !b.interest.IsZero() || !b.free.Equal(d("999.995")) {
		t.Fatalf("the cross account %+v", b)
	}

	// The purge moves BTC in from spot and calls again.
	r.ledger.fund(user, "BTC", d("1"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "in-btc", Direction: domain.DirectionIn, Account: iso, Asset: "BTC", Amount: owed,
	}); err != nil {
		t.Fatal(err)
	}
	res, err = r.svc.Settle(ctx, user, "ops@example.com", "purging a test account")
	if err != nil || res.CanceledOrders != 0 || len(res.Repaid) != 1 || len(res.Remaining) != 0 || !res.Repaid[0].Principal.Equal(owed) ||
		!res.Repaid[0].Interest.IsZero() || !res.Complete() {
		t.Fatalf("settle again %+v %v", res, err)
	}
	if b := r.ledger.owed(user, iso, "BTC"); !b.borrowed.IsZero() || !b.interest.IsZero() || !b.free.IsZero() {
		t.Fatalf("the isolated account's BTC %+v", b)
	}
	res, err = r.svc.Settle(ctx, user, "ops@example.com", "purging a test account")
	if err != nil || res.CanceledOrders != 0 || len(res.Repaid) != 0 || len(res.Remaining) != 0 {
		t.Fatalf("nothing left %+v %v", res, err)
	}
	var made int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM repays WHERE user_id = $1 AND reason = 'AUTO_REPAY' AND status = 'DONE'
		AND idem_key LIKE 'settle:%'`, user).Scan(&made); err != nil || made != 3 {
		t.Fatalf("%d repayments %v", made, err)
	}
	if lent, err := r.store.Read().Pools().Lent(ctx); err != nil || !lent["USDT"].IsZero() || !lent["BTC"].IsZero() {
		t.Fatalf("the pools %v %v", lent, err)
	}
	rows, err := r.db.Query(ctx, `SELECT envelope FROM outbox WHERE event_type = 'audit.AdminActionPerformed'`)
	if err != nil {
		t.Fatal(err)
	}
	audited := 0
	for rows.Next() {
		var envelope []byte
		if err := rows.Scan(&envelope); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(envelope, []byte("margin.user_settled")) && bytes.Contains(envelope, []byte("user:"+user)) &&
			bytes.Contains(envelope, []byte("purging a test account")) {
			audited++
		}
	}
	rows.Close()
	if audited != 3 {
		t.Fatalf("%d settles audited", audited)
	}
	// Its keys are margin-service's own.
	if _, err := r.svc.Repay(ctx, application.RepayInput{UserID: user, IdemKey: application.SettlePrefix + "x", Account: iso, Asset: "BTC", All: true}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("a client's settle: key %v", err)
	}
}

// A cancel the trading service refuses does not stop a settle: the other
// accounts go on and what can be repaid is (review C79 ③). The trading
// service unreachable stops it; what it did is audited all the same,
// with the error (C79 ④).
func TestSettleGoesOnPastARefusedCancel(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user := uuid.Must(uuid.NewV7()).String()
	cross := domain.Cross()
	r.ledger.fund(user, "USDT", d("1000"))
	if _, err := r.svc.Transfer(ctx, application.TransferInput{
		UserID: user, IdemKey: "in", Direction: domain.DirectionIn, Account: cross, Asset: "USDT", Amount: d("1000"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Borrow(ctx, application.BorrowInput{UserID: user, IdemKey: "b", Account: cross, Asset: "USDT", Amount: d("100")}); err != nil {
		t.Fatal(err)
	}
	refusal := apperr.Invalid("not a margin account")
	r.svc.Trading = &trading{ledger: r.ledger, prices: r.prices, cancel: func(string, domain.Account) (int, error) { return 0, refusal }}
	res, err := r.svc.Settle(ctx, user, "ops@example.com", "purging a test account")
	if err != nil || len(res.Repaid) != 1 || !res.Repaid[0].Principal.Equal(d("100")) || !res.Complete() {
		t.Fatalf("settle past a refused cancel %+v %v", res, err)
	}
	audits := func() (n int) {
		t.Helper()
		rows, err := r.db.Query(ctx, `SELECT envelope FROM outbox WHERE event_type = 'audit.AdminActionPerformed'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var envelope []byte
			if err := rows.Scan(&envelope); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(envelope, []byte("margin.user_settled")) && bytes.Contains(envelope, []byte("COMMON_UNAVAILABLE")) {
				n++
			}
		}
		return n
	}
	r.svc.Trading = &trading{ledger: r.ledger, prices: r.prices, cancel: func(string, domain.Account) (int, error) {
		return 0, apperr.Unavailable(context.DeadlineExceeded)
	}}
	if _, err := r.svc.Settle(ctx, user, "ops@example.com", "purging a test account"); code(err) != apperr.CodeUnavailable {
		t.Fatalf("settle with the trading service unreachable: %v", err)
	}
	if n := audits(); n != 1 {
		t.Fatalf("%d failed settles audited", n)
	}
}

// Settle refuses a user with an account being liquidated (the liquidation
// repays it) and a call without an actor and a reason. A user without
// margin accounts - whether or not user-service knows them, it is not
// asked (the coordinator's 06:14 rule) - has nothing to settle: complete.
func TestSettleRefusals(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	user, ghost := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	tr := &trading{ledger: r.ledger, prices: r.prices}
	r.svc.Trading = tr
	iso := domain.Isolated("BTC-USDT")
	err := r.store.Tx(ctx, func(rp ports.Repos) error {
		a, err := rp.Accounts().Ensure(ctx, user, iso, r.now())
		if err != nil {
			return err
		}
		a.Status = domain.StatusLiquidating
		return rp.Accounts().Update(ctx, a)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Settle(ctx, user, "ops@example.com", "purging a test account"); code(err) != "MARGIN_FROZEN" ||
		apperr.From(err).Details["account"] != iso.Key() {
		t.Fatalf("an account being liquidated: %v", err)
	}
	for _, c := range []struct{ user, actor, reason, code string }{
		{"bob", "ops@example.com", "purging a test account", apperr.CodeInvalidArgument},
		{user, "", "purging a test account", apperr.CodeInvalidArgument},
		{user, "ops@example.com", "no", apperr.CodeInvalidArgument},
	} {
		if _, err := r.svc.Settle(ctx, c.user, c.actor, c.reason); code(err) != c.code {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	if tr.canceled != 0 {
		t.Fatalf("%d cancels", tr.canceled)
	}
	res, err := r.svc.Settle(ctx, ghost, "ops@example.com", "purging a test account")
	if err != nil || res.CanceledOrders != 0 || len(res.Repaid) != 0 || len(res.Remaining) != 0 || !res.Complete() || tr.canceled != 0 {
		t.Fatalf("a user without margin accounts %+v %v (%d cancels)", res, err, tr.canceled)
	}
}
