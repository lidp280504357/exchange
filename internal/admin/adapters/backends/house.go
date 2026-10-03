package backends

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Prices returns the last price of every listed symbol that has one
// (market-data-service's tickers); a positive maxAge leaves out those
// updated longer ago (a reference ticker is as of its source, the
// platform's own as of the answer).
func (m Market) Prices(ctx context.Context, maxAge time.Duration) (ports.Prices, error) {
	raw, err := m.do(ctx, http.MethodGet, m.Base+"/v1/market/tickers", nil, nil)
	if err != nil {
		return nil, err
	}
	var body struct {
		Tickers []struct {
			Symbol    string    `json:"symbol"`
			Last      *string   `json:"last"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"tickers"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "market-data answered badly")
	}
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	out := ports.Prices{}
	for _, t := range body.Tickers {
		if t.Last == nil || (maxAge > 0 && now.Sub(t.UpdatedAt) > maxAge) {
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

// Check returns each target's readiness in the targets' order; details
// reads each one's metrics as well.
func (h Health) Check(ctx context.Context, details bool) []ports.ServiceHealth {
	out := make([]ports.ServiceHealth, len(h.Targets))
	var wg sync.WaitGroup
	for i, t := range h.Targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = h.probe(ctx, t)
			if details && out[i].Error != "unreachable" && out[i].Error != "timeout" {
				h.metrics(ctx, t, &out[i])
			}
		}()
	}
	wg.Wait()
	return out
}

// metrics reads a service's version (exchange_build_info), its Kafka
// consumers' lag (kafka_consumer_lag, summed) and the records they parked
// in a DLQ since it started (kafka_consumer_records_total{result="dlq"}).
func (h Health) metrics(ctx context.Context, t HealthTarget, res *ports.ServiceHealth) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL+"/metrics", nil)
	if err != nil {
		return
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var lag, dlq int64
	var consumers bool
	lines := bufio.NewScanner(io.LimitReader(resp.Body, 8<<20))
	lines.Buffer(make([]byte, 64<<10), 1<<20)
	for lines.Scan() {
		name, labels, value, ok := metricLine(lines.Text())
		switch {
		case !ok:
		case name == "exchange_build_info":
			res.Version = labels["version"]
		case name == "kafka_consumer_lag":
			consumers = true
			lag += int64(value)
		case name == "kafka_consumer_records_total":
			consumers = true
			if labels["result"] == "dlq" {
				dlq += int64(value)
			}
		}
	}
	if consumers {
		res.KafkaLag, res.DLQ = &lag, &dlq
	}
}

// metricLine splits a line of the Prometheus text format: name{labels} value.
func metricLine(line string) (string, map[string]string, float64, bool) {
	if line == "" || line[0] == '#' {
		return "", nil, 0, false
	}
	labels := map[string]string{}
	name, rest := line, ""
	if i := strings.IndexByte(line, '{'); i >= 0 {
		j := strings.LastIndexByte(line, '}')
		if j < i {
			return "", nil, 0, false
		}
		name, rest = line[:i], strings.TrimSpace(line[j+1:])
		for _, pair := range strings.Split(line[i+1:j], ",") {
			k, v, found := strings.Cut(pair, "=")
			if found {
				labels[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	} else if k, v, found := strings.Cut(line, " "); found {
		name, rest = k, v
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", nil, 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", nil, 0, false
	}
	return name, labels, value, true
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
