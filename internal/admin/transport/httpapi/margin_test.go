package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/platform/apperr"
)

// TestMarginChangesTakeWholeTerms: a change of margin terms gives every
// field and no other, with the version read; anything else is refused
// before the service is asked (review DL: the cross terms as an asset's
// and a pair's).
func TestMarginChangesTakeWholeTerms(t *testing.T) {
	h := &Handler{}
	const (
		cross = `"leverage":3,"warn_level":"1.3","liquidation_level":"1.1","liquidation_fee":"0.02"`
		asset = `"borrowable":true,"collateral":true,"haircut":"0.95","pool_cap":"100","user_cap":"10","interest_model":"FIXED",` +
			`"fixed_rate":"0.000005","float_base":"0.000005","float_kink":"0.8","float_kink_rate":"0.00003","float_max_rate":"0.0001"`
	)
	for name, c := range map[string]struct {
		handler http.HandlerFunc
		body    string
	}{
		"the cross terms with an unknown field": {h.setMarginSettings, `{"cross":{` + cross + `,"fee":"0.02"},"expected_version":1,"reason":"e2e"}`},
		"the cross terms without the fee": {
			h.setMarginSettings, `{"cross":{"leverage":3,"warn_level":"1.3","liquidation_level":"1.1"},"expected_version":1,"reason":"e2e"}`,
		},
		"the cross terms without the version":    {h.setMarginSettings, `{"cross":{` + cross + `},"reason":"e2e"}`},
		"an asset's terms with an unknown field": {h.setMarginAsset, `{` + asset + `,"borrow":true,"expected_version":1,"reason":"e2e"}`},
		"an asset's terms without borrowable": {
			h.setMarginAsset, `{` + strings.Replace(asset, `"borrowable":true,`, "", 1) + `,"expected_version":1,"reason":"e2e"}`,
		},
		"a pair's terms without isolated": {h.setMarginPair, `{` + cross + `,"expected_version":1,"reason":"e2e"}`},
	} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/admin/v1/margin/settings", strings.NewReader(c.body))
		w := httptest.NewRecorder()
		c.handler(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), apperr.CodeInvalidArgument) {
			t.Errorf("%s: %d %s, want 400 %s", name, w.Code, w.Body, apperr.CodeInvalidArgument)
		}
	}
}
