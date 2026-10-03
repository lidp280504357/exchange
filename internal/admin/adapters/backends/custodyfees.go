package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lidp280504357/exchange/internal/admin/ports"
)

// Fees returns a page of the custodians' withdrawal fees (C6).
func (w Wallet) Fees(ctx context.Context, q ports.FeeQuery) (json.RawMessage, error) {
	v := url.Values{}
	for k, x := range map[string]string{"provider": q.Provider, "status": q.Status, "cursor": q.Cursor} {
		if x != "" {
			v.Set(k, x)
		}
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return w.do(ctx, http.MethodGet, w.Base+"/internal/wallet/custody/fees?"+v.Encode(), nil, nil)
}

// BookFee books a held fee from GAS_SUPPLY, as reported or as found charged.
func (w Wallet) BookFee(ctx context.Context, b ports.FeeBooking) (json.RawMessage, error) {
	body := map[string]any{"actor": b.Actor, "reason": b.Reason}
	if b.Asset != "" {
		body["asset"] = b.Asset
	}
	if b.Amount.IsPositive() {
		body["amount"] = b.Amount.String()
	}
	return w.do(ctx, http.MethodPost, w.Base+"/internal/wallet/custody/fees/"+url.PathEscape(b.WithdrawalID)+"/book", body, nil)
}

// WriteOffFee writes a held fee off.
func (w Wallet) WriteOffFee(ctx context.Context, withdrawalID, actor, reason string) (json.RawMessage, error) {
	return w.do(ctx, http.MethodPost, w.Base+"/internal/wallet/custody/fees/"+url.PathEscape(withdrawalID)+"/write-off",
		map[string]any{"actor": actor, "reason": reason}, nil)
}
