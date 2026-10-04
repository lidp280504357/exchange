package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// Detail returns a withdrawal with what a reviewer weighs.
func (w Wallet) Detail(ctx context.Context, id string) (json.RawMessage, error) {
	return w.do(ctx, http.MethodGet, w.Base+"/internal/wallet/withdrawals/"+url.PathEscape(id), nil, nil)
}

// Hold puts a withdrawal in review on hold, or off hold.
func (w Wallet) Hold(ctx context.Context, id string, hold bool, reviewer, note string) (json.RawMessage, error) {
	return w.do(ctx, http.MethodPost, w.Base+"/internal/wallet/withdrawals/"+url.PathEscape(id)+"/hold",
		map[string]any{"hold": hold, "reviewer": reviewer, "note": note}, nil)
}

// Suspensions lists the assets whose withdrawals are suspended.
func (w Wallet) Suspensions(ctx context.Context) ([]ports.Suspension, error) {
	raw, err := w.do(ctx, http.MethodGet, w.Base+"/internal/wallet/suspensions", nil, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []ports.Suspension `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Items == nil {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "wallet-service answered badly")
	}
	return out.Items, nil
}

// Resume lifts an asset's suspension for actor.
func (w Wallet) Resume(ctx context.Context, asset, actor, reason string) (ports.Suspension, error) {
	raw, err := w.do(ctx, http.MethodPost, w.Base+"/internal/wallet/suspensions/"+url.PathEscape(asset)+"/resume",
		map[string]any{"actor": actor, "reason": reason}, nil)
	if err != nil {
		return ports.Suspension{}, err
	}
	var out ports.Suspension
	if err := json.Unmarshal(raw, &out); err != nil {
		return ports.Suspension{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "wallet-service answered badly")
	}
	return out, nil
}

// List returns a page of deposits; it implements ports.Deposits on the
// Deposits view of the wallet.
func (d WalletDeposits) List(ctx context.Context, q ports.DepositReviewQuery) (json.RawMessage, error) {
	v := url.Values{}
	for k, x := range map[string]string{"user_id": q.UserID, "status": q.Status, "network": q.Network, "cursor": q.Cursor} {
		if x != "" {
			v.Set(k, x)
		}
	}
	if q.Attention {
		v.Set("attention", "true")
	}
	if q.ManualPending {
		v.Set("manual_pending", "true")
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return d.do(ctx, http.MethodGet, d.Base+"/internal/wallet/deposits?"+v.Encode(), nil, nil)
}

// WalletDeposits implements ports.Deposits over wallet-service's internal
// API.
type WalletDeposits struct{ Wallet }

// Get returns one deposit.
func (d WalletDeposits) Get(ctx context.Context, id string) (json.RawMessage, error) {
	return d.do(ctx, http.MethodGet, d.Base+"/internal/wallet/deposits/"+url.PathEscape(id), nil, nil)
}

// Credit gives an unclaimed deposit's funds to its user.
func (d WalletDeposits) Credit(ctx context.Context, id, actor, reason string) (json.RawMessage, error) {
	return d.do(ctx, http.MethodPost, d.Base+"/internal/wallet/deposits/"+url.PathEscape(id)+"/credit",
		map[string]string{"actor": actor, "reason": reason}, nil)
}

// Assign credits a deposit of nobody to userID (C5.5 ㉑).
func (d WalletDeposits) Assign(ctx context.Context, id, userID, actor, reason string) (json.RawMessage, error) {
	return d.do(ctx, http.MethodPost, d.Base+"/internal/wallet/deposits/"+url.PathEscape(id)+"/assign",
		map[string]string{"user_id": userID, "actor": actor, "reason": reason}, nil)
}

// Dismiss closes a deposit that waited for a decision.
func (d WalletDeposits) Dismiss(ctx context.Context, id, actor, reason string) (json.RawMessage, error) {
	return d.do(ctx, http.MethodPost, d.Base+"/internal/wallet/deposits/"+url.PathEscape(id)+"/dismiss",
		map[string]string{"actor": actor, "reason": reason}, nil)
}

func manualBody(m ports.ManualDeposit, reason string) map[string]string {
	return map[string]string{
		"network": m.Network, "trade_id": m.TradeID, "address": m.Address, "tx_hash": m.TxHash, "amount": m.Amount.String(),
		"actor": m.Actor, "reason": reason,
	}
}

// CheckManual checks a backfill without booking it.
func (d WalletDeposits) CheckManual(ctx context.Context, m ports.ManualDeposit) (ports.ManualCheck, error) {
	raw, err := d.do(ctx, http.MethodPost, d.Base+"/internal/wallet/deposits/manual/check", manualBody(m, ""), nil)
	if err != nil {
		return ports.ManualCheck{}, err
	}
	var out ports.ManualCheck
	if err := json.Unmarshal(raw, &out); err != nil || out.UserID == "" || out.Asset == "" {
		return ports.ManualCheck{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "wallet-service answered badly")
	}
	return out, nil
}

// BookManual books a backfill.
func (d WalletDeposits) BookManual(ctx context.Context, m ports.ManualDeposit, reason string) (json.RawMessage, error) {
	return d.do(ctx, http.MethodPost, d.Base+"/internal/wallet/deposits/manual", manualBody(m, reason), nil)
}
