package application

import (
	"github.com/shopspring/decimal"

	orderv1 "github.com/skill/exchange/api/gen/go/exchange/order/v1"
	"github.com/skill/exchange/internal/trading/domain"
)

var (
	sides = map[domain.Side]orderv1.Side{domain.SideBuy: orderv1.Side_SIDE_BUY, domain.SideSell: orderv1.Side_SIDE_SELL}
	types = map[domain.Type]orderv1.OrderType{
		domain.TypeLimit: orderv1.OrderType_ORDER_TYPE_LIMIT, domain.TypeMarket: orderv1.OrderType_ORDER_TYPE_MARKET,
	}
	tifs = map[domain.TimeInForce]orderv1.TimeInForce{
		domain.GTC: orderv1.TimeInForce_TIME_IN_FORCE_GTC, domain.IOC: orderv1.TimeInForce_TIME_IN_FORCE_IOC,
		domain.FOK: orderv1.TimeInForce_TIME_IN_FORCE_FOK, domain.PostOnly: orderv1.TimeInForce_TIME_IN_FORCE_POST_ONLY,
	}
	stps = map[domain.STP]orderv1.SelfTradePrevention{
		domain.CancelNewest: orderv1.SelfTradePrevention_SELF_TRADE_PREVENTION_CANCEL_NEWEST,
		domain.CancelOldest: orderv1.SelfTradePrevention_SELF_TRADE_PREVENTION_CANCEL_OLDEST,
		domain.CancelBoth:   orderv1.SelfTradePrevention_SELF_TRADE_PREVENTION_CANCEL_BOTH,
	}
)

// toProto is the order as the engine receives it.
func toProto(o domain.Order) *orderv1.Order {
	return &orderv1.Order{
		OrderId: o.ID, ClientOrderId: o.ClientOrderID, UserId: o.UserID, Symbol: o.Symbol,
		Side: sides[o.Side], Type: types[o.Type], TimeInForce: tifs[o.TimeInForce], SelfTradePrevention: stps[o.STP],
		Price: amount(o.Price), Quantity: amount(o.Quantity), QuoteAmount: amount(o.QuoteAmount),
		MakerFeeRate: o.MakerFeeRate.String(), TakerFeeRate: o.TakerFeeRate.String(),
		BaseDecimals: o.BaseDecimals, QuoteDecimals: o.QuoteDecimals, ProtectionPrice: amount(o.ProtectionPrice),
		TickSize: amount(o.TickSize), LotSize: amount(o.LotSize), BaseAsset: o.BaseAsset, QuoteAsset: o.QuoteAsset,
		AccountType: string(o.AccountType), SideEffect: string(o.SideEffect),
	}
}

// amount writes a decimal, or "" for an absent (zero) one.
func amount(d decimal.Decimal) string {
	if d.IsZero() {
		return ""
	}
	return d.String()
}
