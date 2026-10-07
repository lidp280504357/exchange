package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// ProductLines asks the services that run the product lines (design
// 2026-10-07, product switches; K1a, K1b) what closing one touches and to
// cancel a closed line's open orders: spot-trading-service for spot,
// derivatives-service for usdt_m and coin_m, both at
// /internal/products/{line} and /internal/products/{line}/cancel-open.
type ProductLines struct {
	REST
	Trading, Derivatives string
}

// lineURL is a line's endpoint at the service that runs it.
func (c ProductLines) lineURL(product string) string {
	base := c.Derivatives
	if product == "spot" {
		base = c.Trading
	}
	return base + "/internal/products/" + url.PathEscape(product)
}

// Line returns a line's open orders and positions.
func (c ProductLines) Line(ctx context.Context, product string) (ports.ProductLine, error) {
	raw, err := c.do(ctx, http.MethodGet, c.lineURL(product), nil, nil)
	if err != nil {
		return ports.ProductLine{}, lineMissing(err)
	}
	var body struct {
		OpenOrders    int `json:"open_orders"`
		OpenPositions int `json:"open_positions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return ports.ProductLine{}, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the service answered badly")
	}
	return ports.ProductLine{OpenOrders: body.OpenOrders, OpenPositions: body.OpenPositions}, nil
}

// CancelOpen cancels a line's open orders: how many it canceled.
func (c ProductLines) CancelOpen(ctx context.Context, product, actor, reason string) (int, error) {
	raw, err := c.do(ctx, http.MethodPost, c.lineURL(product)+"/cancel-open", map[string]string{"actor": actor, "reason": reason}, nil)
	if err != nil {
		return 0, lineMissing(err)
	}
	var out struct {
		Canceled int `json:"canceled"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the service answered badly")
	}
	return out.Canceled, nil
}

// lineMissing is ports.ErrProductLineMissing for a service without the
// route (the platform's router answers "no such endpoint"; anything else a
// 404 without the service's error), err otherwise.
func lineMissing(err error) error {
	if e := apperr.From(err); e != nil && e.Kind == apperr.KindNotFound && (e.Code == apperr.CodeUnavailable || e.Message == "no such endpoint") {
		return ports.ErrProductLineMissing
	}
	return err
}
