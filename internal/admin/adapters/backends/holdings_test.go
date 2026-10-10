package backends_test

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/adapters/backends"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/chx"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/testenv"
	"github.com/skill/exchange/migrations"
)

func TestHoldingsAndTheBotsInLists(t *testing.T) {
	ctx := context.Background()
	cfg := testenv.ClickHouse(t)
	conn, err := chx.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	db := chx.OpenDB(cfg)
	defer db.Close()
	if err := migrate.UpClickHouse(ctx, db, migrations.ClickHouse(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	const (
		b1 = "0192a000-0000-7000-8000-0000000000b1"
		b2 = "0192a000-0000-7000-8000-0000000000b2"
		u1 = "0192a000-0000-7000-8000-0000000000c1"
		u2 = "0192a000-0000-7000-8000-0000000000c2"
		j1 = "0192a000-0000-7000-8000-000000000101"
	)
	// 1,000 ASTRA issued to two bots (one line twice: a redelivered copy);
	// b2 sold 10 to u1, who froze 4 and paid 0.1 in fees; u2 bought 5 and
	// sold them. USDT lines do not count.
	for _, q := range []string{
		`INSERT INTO ledger_entries (journal_id, line_no, owner_type, owner_id, account_type, asset, amount, posted_at) VALUES
			('` + j1 + `', 1, 'SYSTEM', 'SYSTEM', 'ADJUSTMENT', 'ASTRA', -600, '2026-10-01 00:00:00'),
			('` + j1 + `', 2, 'USER', '` + b1 + `', 'SPOT', 'ASTRA', 600, '2026-10-01 00:00:00'),
			(generateUUIDv4(), 1, 'SYSTEM', 'SYSTEM', 'ADJUSTMENT', 'ASTRA', -400, '2026-10-01 00:00:01'),
			(generateUUIDv4(), 2, 'USER', '` + b2 + `', 'SPOT', 'ASTRA', 400, '2026-10-01 00:00:01'),
			(generateUUIDv4(), 1, 'USER', '` + b2 + `', 'SPOT', 'ASTRA', -10, '2026-10-01 00:01:00'),
			(generateUUIDv4(), 2, 'USER', '` + u1 + `', 'SPOT', 'ASTRA', 10, '2026-10-01 00:01:00'),
			(generateUUIDv4(), 1, 'USER', '` + u1 + `', 'SPOT', 'ASTRA', -4, '2026-10-01 00:02:00'),
			(generateUUIDv4(), 2, 'USER', '` + u1 + `', 'SPOT', 'ASTRA', 4, '2026-10-01 00:02:00'),
			(generateUUIDv4(), 1, 'USER', '` + u1 + `', 'SPOT', 'ASTRA', -0.1, '2026-10-01 00:03:00'),
			(generateUUIDv4(), 2, 'SYSTEM', 'SYSTEM', 'FEE_REVENUE', 'ASTRA', 0.1, '2026-10-01 00:03:00'),
			(generateUUIDv4(), 1, 'USER', '` + u2 + `', 'SPOT', 'ASTRA', 5, '2026-10-01 00:04:00'),
			(generateUUIDv4(), 1, 'USER', '` + u2 + `', 'SPOT', 'ASTRA', -5, '2026-10-01 00:05:00'),
			(generateUUIDv4(), 1, 'USER', '` + u2 + `', 'SPOT', 'USDT', 50, '2026-10-01 00:05:00')`,
		`INSERT INTO ledger_entries (journal_id, line_no, owner_type, owner_id, account_type, asset, amount, posted_at) VALUES
			('` + j1 + `', 2, 'USER', '` + b1 + `', 'SPOT', 'ASTRA', 600, '2026-10-01 00:00:00')`,
		// A trade between the bots, one between a bot and a user; an order of each.
		`INSERT INTO trades (trade_id, symbol, price, quantity, quote_quantity, sequence, buyer_user_id, seller_user_id, executed_at) VALUES
			(generateUUIDv4(), 'ASTRA-USDT', 1, 10, 10, 1, '` + b1 + `', '` + b2 + `', now64(3) - 2),
			(generateUUIDv4(), 'ASTRA-USDT', 1, 10, 10, 2, '` + u1 + `', '` + b2 + `', now64(3) - 1)`,
		// Order IDs are UUIDv7: the orders list bounds the time in them (created_key).
		`INSERT INTO order_updates (order_id, user_id, symbol, sequence, status, event_id, occurred_at) VALUES
			(generateUUIDv7(), '` + b1 + `', 'ASTRA-USDT', 1, 'NEW', generateUUIDv4(), now64(3) - 2),
			(generateUUIDv7(), '` + u1 + `', 'ASTRA-USDT', 1, 'NEW', generateUUIDv4(), now64(3) - 1)`,
	} {
		if err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	h, err := backends.Reports{Conn: conn}.Holdings(ctx, "ASTRA", []string{b1, b2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if h.Bots.String() != "990" || h.BotHolders != 2 || h.Users.String() != "9.9" || h.UserHolders != 1 ||
		h.System["ADJUSTMENT"].String() != "-1000" || h.System["FEE_REVENUE"].String() != "0.1" || len(h.System) != 2 {
		t.Fatalf("holdings %+v", h)
	}
	if len(h.Top) != 2 || h.Top[0].UserID != b1 || h.Top[0].Amount.String() != "600" || h.Top[1].UserID != b2 {
		t.Fatalf("top %+v", h.Top)
	}
	if none, err := (backends.Reports{Conn: conn}).Holdings(ctx, "ASTRA", nil, 5); err != nil || none.BotHolders != 0 ||
		none.UserHolders != 3 || none.Users.String() != "999.9" {
		t.Fatalf("without bots everyone is a user %+v %v", none, err)
	}

	records := backends.Records{Conn: conn}
	day := ports.TradeQuery{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Minute), Limit: 10, Bots: []string{b1, b2}}
	for accounts, want := range map[string]int{"": 2, ports.AccountsBots: 1, ports.AccountsUsers: 1} {
		q := day
		q.Accounts = accounts
		trades, _, err := records.Trades(ctx, q)
		if err != nil || len(trades) != want {
			t.Fatalf("trades of %q: %+v %v", accounts, trades, err)
		}
		if accounts == ports.AccountsUsers && trades[0].BuyerUserID != u1 {
			t.Fatalf("a user's trade with a bot is the users' %+v", trades)
		}
		orders, _, err := records.Orders(ctx, ports.OrderQuery{From: day.From, To: day.To, Limit: 10, Bots: day.Bots, Accounts: accounts})
		if err != nil || len(orders) != want {
			t.Fatalf("orders of %q: %+v %v", accounts, orders, err)
		}
		if accounts == ports.AccountsBots && orders[0].UserID != b1 {
			t.Fatalf("the bot's order %+v", orders)
		}
	}

	// The accounts' kinds (L1): the humans leave the other kinds' accounts
	// out, a trade only when both its sides are; a kind keeps its own
	// accounts, a trade when one side is; a kind without accounts keeps
	// nothing. A deposit nobody has claimed is no other kind's.
	if err := conn.Exec(ctx, `INSERT INTO wallet_deposits (deposit_id, user_id, asset, amount, status, unclaimed, updated_at, version) VALUES
		(generateUUIDv7(), '`+b1+`', 'ETH', 1, 'CREDITED', false, now64(3), 1), (generateUUIDv7(), '`+u1+`', 'ETH', 2, 'CREDITED', false, now64(3), 1),
		(generateUUIDv7(), '', 'ETH', 3, 'CONFIRMED', true, now64(3), 1)`); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		f                ports.KindFilter
		trades           int
		orders, deposits []string
	}{
		"the humans":     {ports.KindFilter{Except: []string{b1, b2}}, 1, []string{u1}, []string{"", u1}},
		"the bots":       {ports.KindFilter{Only: []string{b1, b2}}, 2, []string{b1}, []string{b1}},
		"a kind of none": {ports.KindFilter{Only: []string{}}, 0, nil, nil},
		"every kind":     {ports.KindFilter{}, 2, []string{b1, u1}, []string{"", b1, u1}},
	} {
		trades, _, err := records.Trades(ctx, ports.TradeQuery{From: day.From, To: day.To, Limit: 10, ByKind: c.f})
		if err != nil || len(trades) != c.trades {
			t.Fatalf("%s: trades %+v %v", name, trades, err)
		}
		orders, _, err := records.Orders(ctx, ports.OrderQuery{From: day.From, To: day.To, Limit: 10, ByKind: c.f})
		if got := usersOf(orders, func(o ports.Order) string { return o.UserID }); err != nil || !slices.Equal(got, c.orders) {
			t.Fatalf("%s: orders of %q %v", name, got, err)
		}
		deposits, _, err := records.Deposits(ctx, ports.DepositQuery{Limit: 10, ByKind: c.f})
		if got := usersOf(deposits, func(d ports.Deposit) string { return d.UserID }); err != nil || !slices.Equal(got, c.deposits) {
			t.Fatalf("%s: deposits of %q %v", name, got, err)
		}
	}
	// However many accounts a filter carries (past ClickHouse's 256 KiB of
	// query by default).
	many := []string{b1, b2}
	for i := range 8000 {
		many = append(many, fmt.Sprintf("0192a000-0000-7000-8000-%012x", 0x100000+i))
	}
	trades, _, err := records.Trades(ctx, ports.TradeQuery{From: day.From, To: day.To, Limit: 10, ByKind: ports.KindFilter{Except: many}})
	if err != nil || len(trades) != 1 || trades[0].BuyerUserID != u1 {
		t.Fatalf("a long filter: %+v %v", trades, err)
	}
}

// usersOf is the accounts of a list's rows, sorted.
func usersOf[T any](rows []T, user func(T) string) []string {
	var out []string
	for _, r := range rows {
		out = append(out, user(r))
	}
	slices.Sort(out)
	return out
}
