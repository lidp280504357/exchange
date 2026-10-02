package backends

import (
	"context"
	"encoding/json"
	"net/http"
)

// MarketSim reads market-sim's internal API (the simulated market of the
// platform coin, ASTRA design §5.1); its reads need no signature.
type MarketSim struct {
	REST
	Base string
}

// BotUsers returns the user IDs of the simulated market's bots.
func (m MarketSim) BotUsers(ctx context.Context) ([]string, error) {
	raw, err := m.do(ctx, http.MethodGet, m.Base+"/internal/sim", nil, nil)
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
