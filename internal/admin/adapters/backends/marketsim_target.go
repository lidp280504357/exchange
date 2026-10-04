package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lidp280504357/exchange/internal/admin/ports"
)

// Plan returns a threshold target's plan (ASTRA A6).
func (m MarketSim) Plan(ctx context.Context, id string) (json.RawMessage, error) {
	return m.do(ctx, http.MethodGet, m.Base+"/internal/sim/events/"+url.PathEscape(id)+"/plan", nil, nil)
}

// TargetPreview previews a threshold target from the current target (A6).
func (m MarketSim) TargetPreview(ctx context.Context, q ports.SimTargetQuery) (json.RawMessage, error) {
	v := url.Values{"price": {q.Price}, "duration_seconds": {strconv.Itoa(q.DurationSeconds)}}
	if q.Direction != "" {
		v.Set("direction", q.Direction)
	}
	if q.StartsAt != "" {
		v.Set("starts_at", q.StartsAt)
	}
	return m.do(ctx, http.MethodGet, m.Base+"/internal/sim/target-preview?"+v.Encode(), nil, nil)
}
