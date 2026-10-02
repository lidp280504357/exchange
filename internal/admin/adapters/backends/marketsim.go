package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/svcsign"
)

// MarketSim implements ports.Sim on market-sim's internal API (the
// simulated market of the platform coin, docs/runbook/market-sim.md):
// reads need no signature; writes are signed with the console's key
// ("admin", SIM_ADMIN_API_SECRET), the only one market-sim lets name an
// approver.
type MarketSim struct {
	REST
	Base string
	// Signer holds the console's key; without it every write fails.
	Signer svcsign.Client
}

// BotUsers returns the user IDs of the simulated market's bots.
func (m MarketSim) BotUsers(ctx context.Context) ([]string, error) {
	raw, err := m.Status(ctx)
	if err != nil {
		return nil, err
	}
	var st struct {
		Bots []struct {
			UserID string `json:"user_id"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(st.Bots))
	for _, b := range st.Bots {
		out = append(out, b.UserID)
	}
	return out, nil
}

// Status returns the market's state.
func (m MarketSim) Status(ctx context.Context) (json.RawMessage, error) {
	return m.do(ctx, http.MethodGet, m.Base+"/internal/sim", nil, nil)
}

// History returns the target and last price over the last minutes.
func (m MarketSim) History(ctx context.Context, minutes int) (json.RawMessage, error) {
	return m.do(ctx, http.MethodGet, m.Base+"/internal/sim/history?minutes="+strconv.Itoa(minutes), nil, nil)
}

// Events lists the running and scheduled events, or the latest of all.
func (m MarketSim) Events(ctx context.Context, all bool, limit int) (json.RawMessage, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if all {
		q.Set("all", "1")
	}
	return m.do(ctx, http.MethodGet, m.Base+"/internal/sim/events?"+q.Encode(), nil, nil)
}

// CreateEvent creates a price event in the administrators' names.
func (m MarketSim) CreateEvent(ctx context.Context, event map[string]any, actor, approvedBy string) (json.RawMessage, error) {
	body := map[string]any{"actor": actor}
	for k, v := range event {
		body[k] = v
	}
	if approvedBy != "" {
		body["approved_by"] = approvedBy
	}
	return m.signed(ctx, http.MethodPost, "/internal/sim/events", body)
}

// EndEvent cancels a scheduled event or ends a running one.
func (m MarketSim) EndEvent(ctx context.Context, id, actor, reason string) (json.RawMessage, error) {
	return m.signed(ctx, http.MethodPost, "/internal/sim/events/"+url.PathEscape(id)+"/end", map[string]string{"actor": actor, "reason": reason})
}

// UpdateParams replaces the settings in the administrators' names.
func (m MarketSim) UpdateParams(ctx context.Context, params json.RawMessage, actor, approvedBy string) (json.RawMessage, error) {
	body := map[string]any{"params": params, "actor": actor}
	if approvedBy != "" {
		body["approved_by"] = approvedBy
	}
	return m.signed(ctx, http.MethodPut, "/internal/sim/params", body)
}

// signed sends a write with the console's signature over exactly the bytes
// it sends.
func (m MarketSim) signed(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, err
	}
	payload := bytes.TrimRight(b.Bytes(), "\n")
	req, err := http.NewRequestWithContext(ctx, method, m.Base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	signer := m.Signer
	if signer.HTTP == nil {
		signer.HTTP = m.Client
	}
	resp, err := signer.Do(req, payload)
	if err != nil {
		return nil, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "market-sim unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, restError(resp, raw)
	}
	return raw, nil
}
