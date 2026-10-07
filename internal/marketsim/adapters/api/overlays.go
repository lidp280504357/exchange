package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketsim/ports"
	"github.com/skill/exchange/internal/platform/svcsign"
)

// Overlays implements ports.Overlays and ports.House for the price events
// on followed pairs (design 2026-10-07, general price control): pushes of
// the factors to market-data-service (MarketURL), signed with key "sim"
// (Signer: OVERLAY_API_SECRET, J0 contract §2), and HOUSE's rooms from
// market-maker (MarketMakerURL).
type Overlays struct {
	MarketURL      string
	MarketMakerURL string
	Signer         svcsign.Client
	HTTP           *http.Client
}

// OverlayKeyID is the key market-data knows market-sim's pushes by.
const OverlayKeyID = "sim"

func (o *Overlays) get(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return answer(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func answer(resp *http.Response) error {
	var e struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return &Error{Status: resp.StatusCode, Code: e.Code}
}

// Followed reads the pair's reference price from market-data.
func (o *Overlays) Followed(ctx context.Context, symbol string) (ports.Followed, error) {
	var body struct {
		Followed      bool    `json:"followed"`
		Price         *string `json:"price"`
		SourcePrice   *string `json:"source_price"`
		OverlayFactor *string `json:"overlay_factor"`
		Fresh         bool    `json:"fresh"`
	}
	if err := o.get(ctx, o.MarketURL+"/internal/market/"+url.PathEscape(symbol)+"/reference", &body); err != nil {
		return ports.Followed{}, fmt.Errorf("reference %s: %w", symbol, err)
	}
	parse := func(s *string, def decimal.Decimal) (decimal.Decimal, error) {
		if s == nil {
			return def, nil
		}
		return decimal.NewFromString(*s)
	}
	out := ports.Followed{Followed: body.Followed, Fresh: body.Fresh}
	var err error
	if out.Shown, err = parse(body.Price, decimal.Zero); err != nil {
		return ports.Followed{}, fmt.Errorf("reference %s: %w", symbol, err)
	}
	if out.Source, err = parse(body.SourcePrice, out.Shown); err != nil {
		return ports.Followed{}, fmt.Errorf("reference %s: %w", symbol, err)
	}
	if out.Factor, err = parse(body.OverlayFactor, decimal.NewFromInt(1)); err != nil {
		return ports.Followed{}, fmt.Errorf("reference %s: %w", symbol, err)
	}
	return out, nil
}

// Push sets the pair's factor in market-data.
func (o *Overlays) Push(ctx context.Context, symbol string, p ports.OverlayPush) error {
	push := map[string]any{
		"factor": p.Factor.String(), "until": p.Until.UTC().Format(time.RFC3339Nano), "risk": p.Risk, "event_id": p.EventID, "seq": p.Seq,
	}
	if !p.EndsAt.IsZero() {
		push["ends_at"] = p.EndsAt.UTC().Format(time.RFC3339Nano)
	}
	body, err := json.Marshal(push)
	if err != nil {
		return err
	}
	return o.signed(ctx, http.MethodPut, symbol, body)
}

// Clear takes the pair back to 1 in market-data at once.
func (o *Overlays) Clear(ctx context.Context, symbol string) error {
	return o.signed(ctx, http.MethodDelete, symbol, nil)
}

// signed sends a change of the pair's overlay; the signer sets the body.
func (o *Overlays) signed(ctx context.Context, method, symbol string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, method, o.MarketURL+"/internal/market/overlay/"+url.PathEscape(symbol), nil)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	signer := o.Signer
	signer.HTTP = o.HTTP
	resp, err := signer.Do(req, body)
	if err != nil {
		return fmt.Errorf("overlay %s: %w", symbol, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("overlay %s: %w", symbol, answer(resp))
	}
	return nil
}

// Rooms reads HOUSE's rooms on a pair or a contract from market-maker;
// false while HOUSE does not quote it.
func (o *Overlays) Rooms(ctx context.Context, symbol string) (ports.Rooms, bool, error) {
	var body struct {
		Buy       string `json:"buy"`
		Sell      string `json:"sell"`
		UnitValue string `json:"unit_value"`
		Inverse   bool   `json:"inverse"`
	}
	err := o.get(ctx, o.MarketMakerURL+"/internal/house/rooms/"+url.PathEscape(symbol), &body)
	if e := (*Error)(nil); errors.As(err, &e) && e.Status == http.StatusNotFound {
		return ports.Rooms{}, false, nil
	}
	if err != nil {
		return ports.Rooms{}, false, fmt.Errorf("HOUSE rooms %s: %w", symbol, err)
	}
	var ds [3]decimal.Decimal
	for i, s := range []string{body.Buy, body.Sell, body.UnitValue} {
		if ds[i], err = decimal.NewFromString(s); err != nil {
			return ports.Rooms{}, false, fmt.Errorf("HOUSE rooms %s: %w", symbol, err)
		}
	}
	return ports.Rooms{Buy: ds[0], Sell: ds[1], UnitValue: ds[2], Inverse: body.Inverse}, true, nil
}
