package backends

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Prices returns the last price of every listed symbol that has one
// (market-data-service's tickers).
func (m Market) Prices(ctx context.Context) (ports.Prices, error) {
	raw, err := m.do(ctx, http.MethodGet, m.Base+"/v1/market/tickers", nil, nil)
	if err != nil {
		return nil, err
	}
	var body struct {
		Tickers []struct {
			Symbol string  `json:"symbol"`
			Last   *string `json:"last"`
		} `json:"tickers"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "market-data answered badly")
	}
	out := ports.Prices{}
	for _, t := range body.Tickers {
		if t.Last == nil {
			continue
		}
		if p, err := decimal.NewFromString(*t.Last); err == nil && p.IsPositive() {
			out[t.Symbol] = p
		}
	}
	return out, nil
}

// Positions returns a user's open perpetual contract positions, as the
// user's own API renders them.
func (d Derivatives) Positions(ctx context.Context, userID string) (json.RawMessage, error) {
	raw, err := d.do(ctx, http.MethodGet, d.Base+"/v1/derivatives/positions", nil, map[string]string{"X-User-Id": userID})
	if err != nil {
		return nil, err
	}
	var body struct {
		Positions json.RawMessage `json:"positions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Positions == nil {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "derivatives-service answered badly")
	}
	return body.Positions, nil
}

// Reconciliation returns the ledger's latest invariant checks and the
// recent runs with mismatches.
func (l Ledger) Reconciliation(ctx context.Context, failures int) (ports.Reconciliation, error) {
	resp, err := l.C.GetReconciliation(ctx, &ledgerv1.GetReconciliationRequest{Failures: int32(min(failures, 100))}) //nolint:gosec // capped
	if err != nil {
		return ports.Reconciliation{}, err
	}
	conv := func(runs []*ledgerv1.ReconciliationRun) []ports.ReconciliationRun {
		out := make([]ports.ReconciliationRun, 0, len(runs))
		for _, r := range runs {
			at, _ := time.Parse(time.RFC3339Nano, r.GetStartedAt())
			details := json.RawMessage(r.GetDetails())
			if !json.Valid(details) {
				details = json.RawMessage("[]")
			}
			out = append(out, ports.ReconciliationRun{Check: r.GetCheck(), StartedAt: at, Mismatches: int(r.GetMismatches()), Details: details})
		}
		return out
	}
	return ports.Reconciliation{Latest: conv(resp.GetLatest()), Failures: conv(resp.GetFailures())}, nil
}

// housePairs sums HOUSE's side of the spot trades per pair (ADR-0015):
// house_side is the side HOUSE took. The contracts' trades share the
// table; HOUSE's contract side is its positions.
const housePairs = `SELECT symbol, count() AS trades,
		sumIf(quantity, house_side = 'BUY') AS bought, sumIf(quantity, house_side = 'SELL') AS sold,
		sumIf(quote_quantity, house_side = 'BUY') AS paid, sumIf(quote_quantity, house_side = 'SELL') AS got,
		max(executed_at) AS last_at
	FROM trades FINAL WHERE house_side != '' AND NOT endsWith(symbol, '-PERP') GROUP BY symbol ORDER BY symbol`

// HousePairs returns HOUSE's spot trading per pair.
func (r Reports) HousePairs(ctx context.Context) ([]ports.HousePair, error) {
	rows, err := r.Conn.Query(ctx, housePairs)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()
	out := []ports.HousePair{}
	for rows.Next() {
		var p ports.HousePair
		var bought, sold, paid, got decimal.Decimal
		if err := rows.Scan(&p.Symbol, &p.Trades, &bought, &sold, &paid, &got, &p.LastAt); err != nil {
			return nil, unavailable(err)
		}
		p.BoughtBase, p.SoldBase, p.PaidQuote, p.GotQuote = bought.String(), sold.String(), paid.String(), got.String()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// HealthTarget is a service and the base URL of its ops endpoint.
type HealthTarget struct {
	Service string
	URL     string
}

// Health implements ports.Health: it asks every service's /readyz at once,
// each within the client's timeout.
type Health struct {
	Client  *http.Client
	Targets []HealthTarget
}

// Check returns each target's readiness in the targets' order.
func (h Health) Check(ctx context.Context) []ports.ServiceHealth {
	out := make([]ports.ServiceHealth, len(h.Targets))
	var wg sync.WaitGroup
	for i, t := range h.Targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = h.probe(ctx, t)
		}()
	}
	wg.Wait()
	return out
}

func (h Health) probe(ctx context.Context, t HealthTarget) ports.ServiceHealth {
	res := ports.ServiceHealth{Service: t.Service}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL+"/readyz", nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, err := h.Client.Do(req)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = "unreachable"
		if errors.Is(err, context.DeadlineExceeded) {
			res.Error = "timeout"
		}
		return res
	}
	_ = resp.Body.Close()
	res.Ready = resp.StatusCode == http.StatusOK
	if !res.Ready {
		res.Error = resp.Status
	}
	return res
}
