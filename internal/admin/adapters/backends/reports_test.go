package backends_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/admin/adapters/backends"
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
			(generateUUIDv4(), 'FILLED', generateUUIDv4(), 'BTC-USDT-PERP', 100, now64(3)),
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
	trading, err := r.Trading(ctx, 7)
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
	wallet, err := r.Wallet(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(wallet) != 1 || wallet[0].Deposits != 1 || wallet[0].DepositAmount != "0.0012" || wallet[0].Withdrawals != 1 ||
		wallet[0].WithdrawalAmount != "0.0011" || wallet[0].WithdrawalFees != "0.0002" {
		t.Fatalf("wallet report %+v", wallet)
	}
	perps, err := r.Derivatives(ctx, 7)
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
	steps, err := r.Liquidations(ctx, 7, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 4 {
		t.Fatalf("liquidations %+v", steps)
	}
	filled, err := r.Liquidations(ctx, 7, "FILLED", 10)
	if err != nil || len(filled) != 1 || filled[0].InsurancePaid != "100" {
		t.Fatalf("filled %+v %v", filled, err)
	}
	candles, err := r.Candles(ctx, "ETH-BTC", 3600, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candles) != 1 || candles[0].Close != "0.032" || candles[0].Trades != 2 {
		t.Fatalf("candles %+v", candles)
	}
}
