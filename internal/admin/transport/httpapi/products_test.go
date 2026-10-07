package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/skill/exchange/internal/admin/application"
)

// TestProductsChangeJSON: a switch's answer says how its cancel went
// (admin.yaml's ProductCancel, A85-A88): null when it asked none; DONE with
// what it canceled and no reason or error; FAILED with its reason, the
// users failed and the service's own code and message.
func TestProductsChangeJSON(t *testing.T) {
	for name, c := range map[string]struct {
		cancel *application.ProductCancel
		want   string
	}{
		"opened, nothing asked": {nil, `{"canceled_orders":0,"cancel":null}`},
		"closed, canceled": {
			&application.ProductCancel{Status: application.CancelDone, Canceled: 4},
			`{"canceled_orders":4,"cancel":{"status":"DONE","canceled":4,"failed_users":0,"reason":null,"error":null}}`,
		},
		"failed for some users": {
			&application.ProductCancel{
				Status: application.CancelFailed, Canceled: 3, FailedUsers: 2, Reason: application.CancelPartial,
				Error: "COMMON_UNAVAILABLE: service temporarily unavailable",
			},
			`{"canceled_orders":3,"cancel":{"status":"FAILED","canceled":3,"failed_users":2,"reason":"PARTIAL",` +
				`"error":"COMMON_UNAVAILABLE: service temporarily unavailable"}}`,
		},
		"past its time": {
			&application.ProductCancel{Status: application.CancelFailed, Reason: application.CancelTimeout},
			`{"canceled_orders":0,"cancel":{"status":"FAILED","canceled":0,"failed_users":0,"reason":"TIMEOUT","error":null}}`,
		},
	} {
		raw, err := json.Marshal(productsChangeJSON(application.Products{Lines: []application.ProductState{}, Partial: []string{}, Cancel: c.cancel}))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		part, _ := json.Marshal(map[string]json.RawMessage{"canceled_orders": got["canceled_orders"], "cancel": got["cancel"]})
		var want, have any
		_ = json.Unmarshal([]byte(c.want), &want)
		_ = json.Unmarshal(part, &have)
		w, _ := json.Marshal(want)
		h, _ := json.Marshal(have)
		if string(w) != string(h) || string(got["products"]) != "[]" || string(got["partial"]) != "[]" {
			t.Errorf("%s: %s, want %s", name, raw, c.want)
		}
	}
}
