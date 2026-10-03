package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
)

type houseLedger struct{ *fakeLedger }

func (houseLedger) SystemBalances(_ context.Context, asset string) ([]ports.Balance, error) {
	if asset != "" {
		return nil, errors.New("HOUSE reads every asset at once")
	}
	return []ports.Balance{
		{AccountType: "MARKET_MAKER", Asset: "USDT", Available: "400000", Frozen: "0"},
		{AccountType: "MARKET_MAKER", Asset: "BTC", Available: "0.3", Frozen: "0"},
		{AccountType: "MARKET_MAKER", Asset: "SOL", Available: "-10", Frozen: "0"},
		{AccountType: "MARKET_MAKER", Asset: "XYZ", Available: "5", Frozen: "0"}, // no price
		{AccountType: "INSURANCE_FUND", Asset: "USDT", Available: "1000000", Frozen: "0"},
	}, nil
}

type houseCatalog struct{ ports.Instruments }

func (houseCatalog) List(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"assets":[{"asset_code":"USDT","deposit_enabled":false,"withdraw_enabled":true},
		{"asset_code":"BTC","deposit_enabled":true,"withdraw_enabled":true},{"asset_code":"SOL"},{"asset_code":"XYZ"}],"pairs":[],"contracts":[]}`), nil
}

type housePrices ports.Prices

func (p housePrices) Prices(context.Context, time.Duration) (ports.Prices, error) {
	return ports.Prices(p), nil
}

type houseTrades []ports.HousePair

func (t houseTrades) HousePairs(context.Context) ([]ports.HousePair, error) { return t, nil }

type housePositions struct{ user string }

func (h *housePositions) Positions(_ context.Context, userID string) (json.RawMessage, error) {
	h.user = userID
	return json.RawMessage(`[{"symbol":"BTC-USDT-PERP","quantity":"-0.5"}]`), nil
}

func TestHouseValuesItsInventoryAndTrades(t *testing.T) {
	pos := &housePositions{}
	svc := &Service{
		Ledger: houseLedger{&fakeLedger{}}, Catalog: houseCatalog{}, Log: slog.New(slog.DiscardHandler),
		HouseBook: HouseDeps{
			User: "house", Positions: pos,
			Prices: housePrices{"BTC-USDT": decimal.NewFromInt(50000), "SOL-USDT": decimal.NewFromInt(100)},
			Trades: houseTrades{
				// Bought 0.1 for 5,000, sold 0.04 for 2,100: 0.06 BTC (3,000) for 2,900.
				{Symbol: "BTC-USDT", Trades: 3, BoughtBase: "0.1", SoldBase: "0.04", PaidQuote: "5000", GotQuote: "2100"},
				// Sold 10 SOL short for 1,000: worth 1,000 now.
				{Symbol: "SOL-USDT", Trades: 1, BoughtBase: "0", SoldBase: "10", PaidQuote: "0", GotQuote: "1000"},
			},
		},
	}
	h, err := svc.House(context.Background(), Principal{Admin: domain.Admin{Role: domain.RoleAuditor}})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Assets) != 4 || h.Assets[0].Asset != "BTC" || !h.Assets[0].Backed || *h.Assets[0].Value != "15000" ||
		h.Assets[1].Asset != "USDT" || h.Assets[2].Asset != "SOL" || h.Assets[2].Backed || *h.Assets[2].Value != "-1000" ||
		h.Assets[3].Price != nil {
		t.Fatalf("assets %+v", h.Assets)
	}
	if h.Totals != (HouseTotals{Inventory: "414000", Backed: "415000", Internal: "-1000", PnL: "100"}) {
		t.Fatalf("totals %+v", h.Totals)
	}
	if p := h.Pairs[0]; p.NetBase != "0.06" || p.NetQuote != "-2900" || *p.PnL != "100" {
		t.Fatalf("BTC-USDT %+v", p)
	}
	if p := h.Pairs[1]; p.NetBase != "-10" || *p.PnL != "0" {
		t.Fatalf("SOL-USDT %+v", p)
	}
	if pos.user != "house" || string(h.Contracts) != `[{"symbol":"BTC-USDT-PERP","quantity":"-0.5"}]` || len(h.Partial) != 0 {
		t.Fatalf("contracts %s of %q, partial %v", h.Contracts, pos.user, h.Partial)
	}
	if _, err := svc.House(context.Background(), Principal{}); err == nil {
		t.Fatal("a principal without a role read HOUSE's book")
	}
}
