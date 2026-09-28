package domain_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/trading/domain"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var btcUSDT = domain.Pair{
	Symbol: "BTC-USDT", Base: "BTC", Quote: "USDT",
	TickSize: d("0.01"), LotSize: d("0.00001"), MinQuantity: d("0.00001"), MaxQuantity: d("100"),
	MinNotional: d("5"), PriceBand: d("0.1"), MakerFeeRate: d("0.001"), TakerFeeRate: d("0.001"),
	Status: domain.PairTrading, BaseDecimals: 8, QuoteDecimals: 6, Tradable: true,
}

func limit(side domain.Side, price, qty string) domain.Request {
	return domain.Request{UserID: "u", Symbol: "BTC-USDT", Side: side, Type: domain.TypeLimit, Price: d(price), Quantity: d(qty)}
}

func check(t *testing.T, req domain.Request, anchor string) (domain.Order, error) {
	t.Helper()
	return domain.NewOrder("01a0e7de-9e72-74e7-87e8-61cdf564e6a8", req, btcUSDT, d(anchor), time.Now())
}

func TestLimitOrdersFreezeWhatTheyMaySpend(t *testing.T) {
	buy, err := check(t, limit(domain.SideBuy, "60000.01", "0.00013"), "0")
	if err != nil {
		t.Fatal(err)
	}
	// 60000.01 x 0.00013 = 7.8000013 USDT, rounded up to 6 decimals.
	if buy.FrozenAsset != "USDT" || !buy.FrozenAmount.Equal(d("7.800002")) {
		t.Fatalf("buy freezes %s %s", buy.FrozenAmount, buy.FrozenAsset)
	}
	if buy.Status != domain.StatusNew || buy.FreezeState != domain.FreezePending || buy.TimeInForce != domain.GTC ||
		buy.STP != domain.CancelNewest || buy.ClientOrderID != buy.ID || !buy.TakerFeeRate.Equal(d("0.001")) {
		t.Fatalf("defaults: %+v", buy)
	}
	sell, err := check(t, limit(domain.SideSell, "60000", "0.5"), "0")
	if err != nil || sell.FrozenAsset != "BTC" || !sell.FrozenAmount.Equal(d("0.5")) {
		t.Fatalf("sell freezes %s %s (%v)", sell.FrozenAmount, sell.FrozenAsset, err)
	}
}

func TestOrderChecks(t *testing.T) {
	marketBuy := func(quote string) domain.Request {
		return domain.Request{UserID: "u", Symbol: "BTC-USDT", Side: domain.SideBuy, Type: domain.TypeMarket, QuoteAmount: d(quote)}
	}
	for name, tc := range map[string]struct {
		req    domain.Request
		anchor string
		code   string
	}{
		"price off the tick":      {limit(domain.SideBuy, "60000.001", "0.001"), "0", "INSTRUMENT_PRECISION"},
		"quantity off the lot":    {limit(domain.SideBuy, "60000", "0.000011"), "0", "INSTRUMENT_PRECISION"},
		"quantity above the max":  {limit(domain.SideSell, "60000", "101"), "0", "ORDER_QUANTITY_OUT_OF_RANGE"},
		"below the min notional":  {limit(domain.SideBuy, "100", "0.001"), "0", "ORDER_MIN_NOTIONAL"},
		"above the band":          {limit(domain.SideBuy, "66000.01", "0.001"), "60000", "ORDER_PRICE_OUT_OF_BAND"},
		"below the band":          {limit(domain.SideSell, "53999.99", "0.001"), "60000", "ORDER_PRICE_OUT_OF_BAND"},
		"no price":                {limit(domain.SideBuy, "0", "0.001"), "0", apperr.CodeInvalidArgument},
		"market buy by quantity":  {domain.Request{UserID: "u", Side: domain.SideBuy, Type: domain.TypeMarket, Quantity: d("1")}, "0", apperr.CodeInvalidArgument},
		"market buy too precise":  {marketBuy("10.0000001"), "0", "INSTRUMENT_PRECISION"},
		"market buy too small":    {marketBuy("4.99"), "0", "ORDER_MIN_NOTIONAL"},
		"market post only":        {domain.Request{UserID: "u", Side: domain.SideBuy, Type: domain.TypeMarket, QuoteAmount: d("10"), TimeInForce: domain.PostOnly}, "0", apperr.CodeInvalidArgument},
		"market sell with price":  {domain.Request{UserID: "u", Side: domain.SideSell, Type: domain.TypeMarket, Quantity: d("1"), Price: d("1")}, "0", apperr.CodeInvalidArgument},
		"market sell too small":   {domain.Request{UserID: "u", Side: domain.SideSell, Type: domain.TypeMarket, Quantity: d("0.00001")}, "60000", "ORDER_MIN_NOTIONAL"},
		"unknown side":            {limit("HOLD", "60000", "0.001"), "0", apperr.CodeInvalidArgument},
		"unknown type":            {domain.Request{UserID: "u", Side: domain.SideBuy, Type: "STOP"}, "0", apperr.CodeInvalidArgument},
		"unknown time in force":   {domain.Request{UserID: "u", Side: domain.SideBuy, Type: domain.TypeLimit, TimeInForce: "GTD", Price: d("60000"), Quantity: d("0.001")}, "0", apperr.CodeInvalidArgument},
		"bad client order id":     {domain.Request{UserID: "u", ClientOrderID: "has space", Side: domain.SideBuy, Type: domain.TypeLimit, Price: d("60000"), Quantity: d("0.001")}, "0", apperr.CodeInvalidArgument},
		"unknown self-trade mode": {domain.Request{UserID: "u", STP: "ALLOW", Side: domain.SideBuy, Type: domain.TypeLimit, Price: d("60000"), Quantity: d("0.001")}, "0", apperr.CodeInvalidArgument},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := check(t, tc.req, tc.anchor); !apperr.Is(err, tc.code) {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
		})
	}
}

func TestPairsThatDoNotTrade(t *testing.T) {
	for _, pair := range []domain.Pair{
		func() domain.Pair { p := btcUSDT; p.Status = "HALT"; return p }(),
		func() domain.Pair { p := btcUSDT; p.Tradable = false; return p }(),
	} {
		if _, err := domain.NewOrder("id", limit(domain.SideBuy, "60000", "0.001"), pair, decimal.Zero, time.Now()); !apperr.Is(err, "INSTRUMENT_NOT_TRADING") {
			t.Fatalf("%+v: %v", pair, err)
		}
	}
}

func TestMarketOrdersCarryTheirProtection(t *testing.T) {
	buy, err := check(t, domain.Request{UserID: "u", Side: domain.SideBuy, Type: domain.TypeMarket, QuoteAmount: d("100")}, "60000.05")
	if err != nil {
		t.Fatal(err)
	}
	// 60000.05 x 1.1 = 66000.055, down to the tick.
	if buy.TimeInForce != domain.IOC || !buy.ProtectionPrice.Equal(d("66000.05")) || buy.FrozenAsset != "USDT" || !buy.FrozenAmount.Equal(d("100")) {
		t.Fatalf("market buy: %+v", buy)
	}
	sell, err := check(t, domain.Request{UserID: "u", Side: domain.SideSell, Type: domain.TypeMarket, Quantity: d("0.01"), TimeInForce: domain.FOK}, "60000.05")
	if err != nil {
		t.Fatal(err)
	}
	// 60000.05 x 0.9 = 54000.045, up to the tick.
	if !sell.ProtectionPrice.Equal(d("54000.05")) || sell.FrozenAsset != "BTC" || !sell.FrozenAmount.Equal(d("0.01")) {
		t.Fatalf("market sell: %+v", sell)
	}
	unbounded, err := check(t, domain.Request{UserID: "u", Side: domain.SideSell, Type: domain.TypeMarket, Quantity: d("0.00001")}, "0")
	if err != nil || !unbounded.ProtectionPrice.IsZero() {
		t.Fatalf("without an anchor: %+v %v", unbounded, err)
	}
}

func TestStateMachine(t *testing.T) {
	all := []domain.Status{
		domain.StatusNew, domain.StatusOpen, domain.StatusPartiallyFilled, domain.StatusFilled,
		domain.StatusCanceled, domain.StatusRejected, domain.StatusExpired,
	}
	allowed := map[[2]domain.Status]bool{}
	for _, p := range [][2]domain.Status{
		{"NEW", "OPEN"},
		{"NEW", "PARTIALLY_FILLED"},
		{"NEW", "FILLED"},
		{"NEW", "CANCELED"},
		{"NEW", "REJECTED"},
		{"OPEN", "PARTIALLY_FILLED"},
		{"OPEN", "FILLED"},
		{"OPEN", "CANCELED"},
		{"OPEN", "EXPIRED"},
		{"PARTIALLY_FILLED", "PARTIALLY_FILLED"},
		{"PARTIALLY_FILLED", "FILLED"},
		{"PARTIALLY_FILLED", "CANCELED"},
		{"PARTIALLY_FILLED", "EXPIRED"},
	} {
		allowed[p] = true
	}
	for _, from := range all {
		for _, to := range all {
			if got := domain.CanTransition(from, to); got != allowed[[2]domain.Status{from, to}] {
				t.Errorf("%s -> %s: %v", from, to, got)
			}
		}
	}
	if !domain.StatusPartiallyFilled.Active() || domain.StatusFilled.Active() {
		t.Fatal("active statuses")
	}
}

func TestSameAs(t *testing.T) {
	o, err := check(t, limit(domain.SideBuy, "60000", "0.001"), "0")
	if err != nil {
		t.Fatal(err)
	}
	if !o.SameAs(limit(domain.SideBuy, "60000.00", "0.0010")) {
		t.Fatal("equal amounts written differently, with the defaults left out, ask for the same order")
	}
	explicit := limit(domain.SideBuy, "60000", "0.001")
	explicit.TimeInForce, explicit.STP = domain.GTC, domain.CancelNewest
	if !o.SameAs(explicit) {
		t.Fatal("spelled-out defaults ask for the same order")
	}
	if o.SameAs(limit(domain.SideBuy, "60001", "0.001")) || o.SameAs(limit(domain.SideSell, "60000", "0.001")) {
		t.Fatal("another price or side is another order")
	}
}
