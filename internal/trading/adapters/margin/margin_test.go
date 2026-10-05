package margin

import (
	"testing"

	"github.com/shopspring/decimal"

	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/trading/domain"
)

func TestCheckCarriesWhatMarginServiceWeighs(t *testing.T) {
	d := decimal.RequireFromString
	limit := Check(domain.Order{
		ID: "o1", UserID: "u1", Symbol: "BTC-USDT", Side: domain.SideSell, Type: domain.TypeLimit,
		Price: d("61000"), Quantity: d("0.5"), FrozenAsset: "BTC", FrozenAmount: d("0.5"),
		AccountType: domain.AccountMarginIsolated, SideEffect: domain.SideEffectAutoBorrow,
	})
	if limit.GetOrderSide() != orderv1.Side_SIDE_SELL || limit.GetPrice() != "61000" || limit.GetQuantity() != "0.5" ||
		limit.GetFreezeAsset() != "BTC" || limit.GetFreezeAmount() != "0.5" || limit.GetAccountType() != "MARGIN_ISOLATED" ||
		limit.GetSideEffect() != "AUTO_BORROW" || limit.GetOrderId() != "o1" || limit.GetSymbol() != "BTC-USDT" {
		t.Fatalf("limit sell: %v", limit)
	}
	// A market buy by quote amount has neither price nor quantity.
	market := Check(domain.Order{
		ID: "o2", UserID: "u1", Symbol: "BTC-USDT", Side: domain.SideBuy, Type: domain.TypeMarket,
		QuoteAmount: d("100"), FrozenAsset: "USDT", FrozenAmount: d("100"), AccountType: domain.AccountMarginCross,
		SideEffect: domain.SideEffectNone,
	})
	if market.GetOrderSide() != orderv1.Side_SIDE_BUY || market.GetPrice() != "" || market.GetQuantity() != "" ||
		market.GetFreezeAmount() != "100" {
		t.Fatalf("market buy: %v", market)
	}
}
