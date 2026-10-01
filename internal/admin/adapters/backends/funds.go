package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/lidp280504357/exchange/internal/admin/ports"
)

func rfc3339(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func holdOf(h *ledgerv1.Hold) ports.Hold {
	return ports.Hold{
		ID: h.GetHoldId(), UserID: h.GetUserId(), AccountType: h.GetAccountType(), Asset: h.GetAsset(), Amount: h.GetAmount(),
		Reason: h.GetReason(), Actor: h.GetActor(), JournalID: h.GetJournalId(), CreatedAt: rfc3339(h.GetCreatedAt()),
		ReleasedAt: rfc3339(h.GetReleasedAt()), ReleasedBy: h.GetReleasedBy(), ReleaseReason: h.GetReleaseReason(),
		ReleaseJournalID: h.GetReleaseJournalId(),
	}
}

// PlaceHold freezes part of a user's SPOT balance.
func (l Ledger) PlaceHold(ctx context.Context, id, userID, asset string, amount decimal.Decimal, actor, reason string) (ports.Hold, error) {
	resp, err := l.C.PlaceHold(ctx, &ledgerv1.PlaceHoldRequest{
		HoldId: id, UserId: userID, Asset: asset, Amount: amount.String(), Actor: actor, Reason: reason,
	})
	if err != nil {
		return ports.Hold{}, err
	}
	return holdOf(resp.GetHold()), nil
}

// ReleaseHold releases a hold.
func (l Ledger) ReleaseHold(ctx context.Context, id, actor, reason string) (ports.Hold, error) {
	resp, err := l.C.ReleaseHold(ctx, &ledgerv1.ReleaseHoldRequest{HoldId: id, Actor: actor, Reason: reason})
	if err != nil {
		return ports.Hold{}, err
	}
	return holdOf(resp.GetHold()), nil
}

// Holds lists a user's holds.
func (l Ledger) Holds(ctx context.Context, userID string, activeOnly bool) ([]ports.Hold, error) {
	resp, err := l.C.ListHolds(ctx, &ledgerv1.ListHoldsRequest{UserId: userID, ActiveOnly: activeOnly})
	if err != nil {
		return nil, err
	}
	out := make([]ports.Hold, 0, len(resp.GetHolds()))
	for _, h := range resp.GetHolds() {
		out = append(out, holdOf(h))
	}
	return out, nil
}

// Cancel asks the engine to cancel one of a user's spot orders.
func (t Trading) Cancel(ctx context.Context, userID, orderID string) (json.RawMessage, error) {
	return t.do(ctx, http.MethodDelete, t.Base+"/v1/orders/"+url.PathEscape(orderID), nil, map[string]string{"X-User-Id": userID})
}

// OpenOrders returns a user's active contract orders.
func (d Derivatives) OpenOrders(ctx context.Context, userID string) (json.RawMessage, error) {
	return d.do(ctx, http.MethodGet, d.Base+"/v1/derivatives/orders?status=ACTIVE&limit=100", nil, map[string]string{"X-User-Id": userID})
}

// CancelOrder asks the engine to cancel one of a user's contract orders.
func (d Derivatives) CancelOrder(ctx context.Context, userID, orderID string) (json.RawMessage, error) {
	return d.do(ctx, http.MethodDelete, d.Base+"/v1/derivatives/orders/"+url.PathEscape(orderID), nil, map[string]string{"X-User-Id": userID})
}

// ClosePosition closes a user's position at the market.
func (d Derivatives) ClosePosition(ctx context.Context, userID, symbol, positionSide, clientOrderID string) (json.RawMessage, error) {
	return d.do(ctx, http.MethodPost, d.Base+"/internal/derivatives/positions/close", map[string]string{
		"user_id": userID, "symbol": strings.ToUpper(symbol), "position_side": strings.ToUpper(positionSide), "client_order_id": clientOrderID,
	}, nil)
}
