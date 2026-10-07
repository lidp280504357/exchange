package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// ProductLines asks the services that run the product lines (design
// 2026-10-07, product switches; K1a, K1b; api/internal/products.yaml) what
// closing one touches and to cancel a closed line's open orders:
// spot-trading-service for spot, derivatives-service for usdt_m and
// coin_m, both at /internal/products/{line} and
// /internal/products/{line}/cancel-open.
type ProductLines struct {
	REST
	Trading, Derivatives string
	// ReadTimeout and CancelTimeout bound a count and a cancel (defaults
	// DefaultLineReadTimeout and DefaultLineCancelTimeout).
	ReadTimeout, CancelTimeout time.Duration
}

// A switch reads the counts, cancels and reads them again within the
// console's own 30-second write timeout (and the services answer within
// theirs, 30 seconds too): a cancel that takes longer is told as failed,
// the line closed, and run again it cancels what is left (A85).
const (
	DefaultLineReadTimeout   = 5 * time.Second
	DefaultLineCancelTimeout = 15 * time.Second
)

// bounded is ctx bounded by d, or by def when d is 0.
func bounded(ctx context.Context, d, def time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = def
	}
	return context.WithTimeout(ctx, d)
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
	ctx, cancel := bounded(ctx, c.ReadTimeout, DefaultLineReadTimeout)
	defer cancel()
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

// CancelOpen cancels a line's open orders: how many it canceled - with an
// error, those the service said it canceled before failing
// (derivatives-service's 503 names them, C60).
func (c ProductLines) CancelOpen(ctx context.Context, product, actor, reason string) (int, error) {
	ctx, cancel := bounded(ctx, c.CancelTimeout, DefaultLineCancelTimeout)
	defer cancel()
	raw, err := c.do(ctx, http.MethodPost, c.lineURL(product)+"/cancel-open", map[string]string{"actor": actor, "reason": reason}, nil)
	if err != nil {
		n := 0
		if e := apperr.From(err); e != nil {
			if v, ok := e.Details["canceled"].(float64); ok {
				n = int(v)
			}
		}
		return n, lineMissing(err)
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
