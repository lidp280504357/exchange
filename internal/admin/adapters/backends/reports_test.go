package backends_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/admin/adapters/backends"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/chx"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func TestReports(t *testing.T) {
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
	now := time.Now().UTC()
	today := now.Format(time.DateOnly)
	midnight := now.Truncate(24 * time.Hour)
	for _, q := range []string{
		`INSERT INTO trades (trade_id, symbol, price, quantity, quote_quantity, sequence, executed_at) VALUES
			(generateUUIDv4(), 'ETH-BTC', 0.03, 1, 0.03, 1, now64(3)), (generateUUIDv4(), 'ETH-BTC', 0.032, 2, 0.064, 2, now64(3)),
			(generateUUIDv4(), 'ETH-BTC', 0.02, 5, 0.1, 0, now64(3) - INTERVAL 30 DAY)`,
		`INSERT INTO order_updates (order_id, symbol, sequence, status, event_id, occurred_at) VALUES
			(generateUUIDv4(), 'ETH-BTC', 0, 'NEW', generateUUIDv4(), now64(3)),
			(generateUUIDv4(), 'BTC-USDT', 0, 'REJECTED', generateUUIDv4(), now64(3))`,
		`INSERT INTO wallet_deposits (deposit_id, asset, amount, status, unclaimed, updated_at, version) VALUES
			(generateUUIDv4(), 'ETH', 0.0012, 'CREDITED', false, now64(3), 1), (generateUUIDv4(), 'ETH', 0.0001, 'CREDITED', true, now64(3), 1),
			(generateUUIDv4(), 'ETH', 5, 'DETECTED', false, now64(3), 1)`,
		`INSERT INTO wallet_withdrawals (withdrawal_id, asset, amount, fee, status, updated_at, version) VALUES
			(generateUUIDv4(), 'ETH', 0.0011, 0.0002, 'CONFIRMED', now64(3), 1)`,
		`INSERT INTO candles_1m (symbol, open_time, open, high, low, close, volume, quote_volume, trades, updated_at) VALUES
			('ETH-BTC', toStartOfMinute(now()), 0.03, 0.032, 0.03, 0.032, 3, 0.094, 2, now64(3))`,
		`INSERT INTO derivatives_fills (trade_id, order_id, user_id, symbol, side, price, quantity, notional, fee, realized_pnl,
			executed_at) VALUES
			('0192a000-0000-7000-8000-000000000001', generateUUIDv4(), generateUUIDv4(), 'BTC-USDT-PERP', 'BUY', 60000, 0.5, 30000, 6, 0, now64(3)),
			('0192a000-0000-7000-8000-000000000001', generateUUIDv4(), generateUUIDv4(), 'BTC-USDT-PERP', 'SELL', 60000, 0.5, 30000, 15, -20, now64(3))`,
		`INSERT INTO derivatives_funding (position_id, user_id, symbol, funding_time, amount, settled_at) VALUES
			(generateUUIDv4(), generateUUIDv4(), 'BTC-USDT-PERP', toStartOfHour(now()), -3, now64(3)),
			(generateUUIDv4(), generateUUIDv4(), 'BTC-USDT-PERP', toStartOfHour(now()), 2.5, now64(3))`,
		`INSERT INTO derivatives_liquidations (event_id, kind, user_id, symbol, insurance_paid, occurred_at) VALUES
			(generateUUIDv4(), 'STARTED', generateUUIDv4(), 'BTC-USDT-PERP', 0, now64(3)),
			(generateUUIDv4(), 'FILLED', '0192a000-0000-7000-8000-0000000000b7', 'BTC-USDT-PERP', 100, now64(3)),
			(generateUUIDv4(), 'ADL', generateUUIDv4(), 'BTC-USDT-PERP', 0, now64(3)),
			(generateUUIDv4(), 'WARNING', generateUUIDv4(), '', 0, now64(3))`,
		`INSERT INTO derivatives_positions (position_id, user_id, symbol, quantity, updated_at, version) VALUES
			('0192a000-0000-7000-8000-0000000000a1', generateUUIDv4(), 'BTC-USDT-PERP', 0.5, now64(3), 1),
			('0192a000-0000-7000-8000-0000000000a1', generateUUIDv4(), 'BTC-USDT-PERP', 0.7, now64(3), 2),
			(generateUUIDv4(), generateUUIDv4(), 'BTC-USDT-PERP', -0.7, now64(3), 3),
			(generateUUIDv4(), generateUUIDv4(), 'ETH-USDT-PERP', 0, now64(3), 3)`,
	} {
		if err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	r := backends.Reports{Conn: conn}
	week := ports.ReportRange{From: midnight.AddDate(0, 0, -6), To: midnight, Bucket: ports.BucketDay}
	trading, err := r.Trading(ctx, week)
	if err != nil {
		t.Fatal(err)
	}
	if len(trading) != 2 {
		t.Fatalf("trading report %+v", trading)
	}
	for _, d := range trading {
		switch d.Symbol {
		case "ETH-BTC":
			if d.Day != today || d.Trades != 2 || d.Volume != "3" || d.QuoteVolume != "0.094" || d.Orders != 1 || d.Rejected != 0 {
				t.Fatalf("ETH-BTC %+v", d)
			}
		case "BTC-USDT":
			if d.Trades != 0 || d.Volume != "0" || d.Rejected != 1 {
				t.Fatalf("BTC-USDT %+v", d)
			}
		default:
			t.Fatalf("unexpected %+v", d)
		}
	}
	wallet, err := r.Wallet(ctx, week)
	if err != nil {
		t.Fatal(err)
	}
	if len(wallet) != 1 || wallet[0].Deposits != 1 || wallet[0].DepositAmount != "0.0012" || wallet[0].Withdrawals != 1 ||
		wallet[0].WithdrawalAmount != "0.0011" || wallet[0].WithdrawalFees != "0.0002" {
		t.Fatalf("wallet report %+v", wallet)
	}
	perps, err := r.Derivatives(ctx, week)
	if err != nil {
		t.Fatal(err)
	}
	if len(perps) != 1 || perps[0].Day != today || perps[0].Fills != 2 || perps[0].Volume != "0.5" || perps[0].Notional != "30000" ||
		perps[0].Fees != "21" || perps[0].RealizedPnL != "-20" || perps[0].FundingPaid != "3" || perps[0].FundingReceived != "2.5" ||
		perps[0].Liquidations != 1 || perps[0].ADL != 1 || perps[0].InsurancePaid != "100" {
		t.Fatalf("derivatives report %+v", perps)
	}
	oi, err := r.OpenInterest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(oi) != 1 || oi[0].Symbol != "BTC-USDT-PERP" || oi[0].Long != "0.7" || oi[0].Short != "0.7" || oi[0].Positions != 2 {
		t.Fatalf("open interest %+v", oi)
	}
	steps, next, err := r.Liquidations(ctx, ports.LiquidationQuery{Days: 7, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 4 || next != "" {
		t.Fatalf("liquidations %+v %q", steps, next)
	}
	firstTwo, next, err := r.Liquidations(ctx, ports.LiquidationQuery{Days: 7, Limit: 2})
	if err != nil || len(firstTwo) != 2 || next == "" {
		t.Fatalf("first page %+v %q %v", firstTwo, next, err)
	}
	rest, last, err := r.Liquidations(ctx, ports.LiquidationQuery{Days: 7, Cursor: next, Limit: 2})
	if err != nil || len(rest) != 2 || last != "" || rest[0].EventID == firstTwo[1].EventID {
		t.Fatalf("second page %+v %q %v", rest, last, err)
	}
	filled, _, err := r.Liquidations(ctx, ports.LiquidationQuery{Days: 7, Kind: "FILLED", Limit: 10})
	if err != nil || len(filled) != 1 || filled[0].InsurancePaid != "100" {
		t.Fatalf("filled %+v %v", filled, err)
	}
	if btc, _, err := r.Liquidations(ctx, ports.LiquidationQuery{Days: 7, Symbol: "BTC-USDT-PERP", Limit: 10}); err != nil || len(btc) != 3 {
		t.Fatalf("of a contract %+v %v", btc, err)
	}
	mine, _, err := r.Liquidations(ctx, ports.LiquidationQuery{Days: 7, UserID: "0192a000-0000-7000-8000-0000000000b7", Limit: 10})
	if err != nil || len(mine) != 1 || mine[0].Kind != "FILLED" {
		t.Fatalf("of a user %+v %v", mine, err)
	}
	candles, err := r.Candles(ctx, "ETH-BTC", 3600, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candles) != 1 || candles[0].Close != "0.032" || candles[0].Trades != 2 {
		t.Fatalf("candles %+v", candles)
	}
}

func TestHousePairs(t *testing.T) {
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
	// HOUSE sold 2 SOL for 200 and bought 0.5 back for 49; a trade between
	// users does not count.
	if err := conn.Exec(ctx, `INSERT INTO trades (trade_id, symbol, price, quantity, quote_quantity, sequence, executed_at, house_side) VALUES
		(generateUUIDv4(), 'SOL-USDT', 100, 2, 200, 1, now64(3), 'SELL'), (generateUUIDv4(), 'SOL-USDT', 98, 0.5, 49, 2, now64(3), 'BUY'),
		(generateUUIDv4(), 'SOL-USDT', 99, 1, 99, 3, now64(3), '')`); err != nil {
		t.Fatal(err)
	}
	pairs, err := backends.Reports{Conn: conn}.HousePairs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].Symbol != "SOL-USDT" || pairs[0].Trades != 2 || pairs[0].SoldBase != "2" || pairs[0].BoughtBase != "0.5" ||
		pairs[0].GotQuote != "200" || pairs[0].PaidQuote != "49" || pairs[0].LastAt.IsZero() {
		t.Fatalf("house pairs %+v", pairs)
	}
}

func TestUsersAndHouseReports(t *testing.T) {
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
		house = "0192a000-0000-7000-8000-0000000000aa"
		bot   = "0192a000-0000-7000-8000-0000000000bb"
		u1    = "0192a000-0000-7000-8000-0000000000c1"
		u2    = "0192a000-0000-7000-8000-0000000000c2"
	)
	for _, q := range []string{
		// u1 registers and signs in twice today, the bot registers; u2
		// registered 30 days ago.
		`INSERT INTO events (event_id, event_type, topic, aggregate_type, aggregate_id, occurred_at, payload) VALUES
			(generateUUIDv4(), 'auth.UserRegistered', 'auth.events', 'user', '` + u1 + `', now64(3), '{}'),
			(generateUUIDv4(), 'auth.LoginSucceeded', 'auth.events', 'user', '` + u1 + `', now64(3), '{}'),
			(generateUUIDv4(), 'auth.LoginSucceeded', 'auth.events', 'user', '` + u1 + `', now64(3), '{}'),
			(generateUUIDv4(), 'auth.UserRegistered', 'auth.events', 'user', '` + bot + `', now64(3), '{}'),
			(generateUUIDv4(), 'auth.UserRegistered', 'auth.events', 'user', '` + u2 + `', now64(3) - INTERVAL 30 DAY, '{}')`,
		// HOUSE bought 1 BTC from u1 at 50,000 two days ago and sold u2 0.5
		// at 51,000 today; the bot traded with itself at 52,000 last.
		`INSERT INTO trades (trade_id, symbol, price, quantity, quote_quantity, sequence, buyer_user_id, seller_user_id, house_side,
			executed_at) VALUES
			(generateUUIDv4(), 'BTC-USDT', 50000, 1, 50000, 1, '` + house + `', '` + u1 + `', 'BUY', now64(3) - INTERVAL 2 DAY),
			(generateUUIDv4(), 'BTC-USDT', 51000, 0.5, 25500, 2, '` + u2 + `', '` + house + `', 'SELL', now64(3) - INTERVAL 1 SECOND),
			(generateUUIDv4(), 'BTC-USDT', 52000, 1, 52000, 3, '` + bot + `', '` + bot + `', '', now64(3))`,
		`INSERT INTO derivatives_fills (trade_id, order_id, user_id, symbol, side, price, quantity, notional, fee, realized_pnl, executed_at) VALUES
			(generateUUIDv4(), generateUUIDv4(), '` + house + `', 'BTC-USDT-PERP', 'SELL', 60000, 0.1, 6000, 0, 40, now64(3)),
			(generateUUIDv4(), generateUUIDv4(), '` + u1 + `', 'BTC-USDT-PERP', 'BUY', 60000, 0.1, 6000, 3, 0, now64(3))`,
		`INSERT INTO derivatives_funding (position_id, user_id, symbol, funding_time, amount, settled_at) VALUES
			(generateUUIDv4(), '` + house + `', 'BTC-USDT-PERP', toStartOfHour(now()), -2.5, now64(3))`,
		`INSERT INTO wallet_deposits (deposit_id, user_id, asset, amount, status, unclaimed, updated_at, version) VALUES
			(generateUUIDv4(), '` + u2 + `', 'USDT', 10, 'CREDITED', false, now64(3), 1),
			(generateUUIDv4(), '` + u2 + `', 'USDT', 5, 'CREDITED', false, now64(3), 1)`,
	} {
		if err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	r := backends.Reports{Conn: conn}
	midnight := time.Now().UTC().Truncate(24 * time.Hour)
	today := ports.ReportRange{From: midnight, To: midnight, Bucket: ports.BucketDay}
	users, before, err := r.Users(ctx, today, []string{house, bot})
	if err != nil {
		t.Fatal(err)
	}
	// u1 registered, signed in and traded a contract; u2 traded spot and got
	// two deposits; HOUSE and the bot are left out.
	if before != 1 || len(users) != 1 || users[0] != (ports.UsersBucket{
		Day: midnight.Format(time.DateOnly), Registered: 1, SignedIn: 1, Traders: 2, Depositors: 1,
	}) {
		t.Fatalf("users %+v before %d", users, before)
	}
	spot, err := r.HouseSpot(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	if len(spot) != 2 || !spot[0].BeforePeriod || spot[0].NetBase.String() != "1" || spot[0].NetQuote.String() != "-50000" ||
		spot[0].Close.String() != "50000" || spot[1].BeforePeriod || spot[1].NetBase.String() != "-0.5" || spot[1].NetQuote.String() != "25500" ||
		spot[1].Close.String() != "52000" || spot[1].QuoteAsset != "USDT" || !spot[1].Day.Equal(midnight) {
		t.Fatalf("house spot %+v", spot)
	}
	contracts, err := r.HouseContracts(ctx, today, house)
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 1 || contracts[0].Realized.String() != "40" || contracts[0].Funding.String() != "-2.5" {
		t.Fatalf("house contracts %+v", contracts)
	}
	month := ports.ReportRange{From: midnight.AddDate(0, 0, -2), To: midnight, Bucket: ports.BucketMonth}
	trading, err := r.Trading(ctx, month)
	if err != nil {
		t.Fatal(err)
	}
	trades := uint64(0)
	for _, d := range trading {
		if d.Day[8:] != "01" {
			t.Fatalf("a month's row on %s", d.Day)
		}
		trades += d.Trades
	}
	if trades != 3 {
		t.Fatalf("by month %+v", trading)
	}
}
