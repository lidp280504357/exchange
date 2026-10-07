package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/svcsign"
)

// MarketMaker is market-maker's internal API (review C45): HOUSE's caps
// at run time, read and changed for the console. Reads need no signature;
// a change is signed with the console's key ("admin",
// HOUSE_CAPS_ADMIN_API_SECRET), the only key market-maker lets name an
// approver (review C47).
type MarketMaker struct {
	REST
	Base string
	// Signer holds the console's key; without it every change fails,
	// unavailable until it is set.
	Signer svcsign.Client
}

// errNoCapsKey refuses a change admin-service cannot sign.
var errNoCapsKey = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable,
	"admin-service cannot sign changes of HOUSE's caps: HOUSE_CAPS_ADMIN_API_SECRET is not set")

// HouseCaps returns the caps as market-maker keeps them.
func (m MarketMaker) HouseCaps(ctx context.Context) (json.RawMessage, error) {
	return m.do(ctx, http.MethodGet, m.Base+"/internal/house/caps", nil, nil)
}

// SetHouseCaps changes the caps given of the version read, in the
// requester's name.
func (m MarketMaker) SetHouseCaps(ctx context.Context, w ports.HouseCapsWrite) (json.RawMessage, error) {
	body := map[string]any{"version": w.Version, "actor": w.Actor, "approver": w.Approver, "approval_id": w.ApprovalID, "reason": w.Reason}
	for k, v := range w.Caps {
		body[k] = v
	}
	if len(m.Signer.Secret) == 0 {
		return nil, errNoCapsKey
	}
	return m.signed(ctx, m.Signer, http.MethodPut, m.Base+"/internal/house/caps", body, "market-maker unreachable")
}

// HouseCapsChanges returns the latest changes of the caps, newest first.
func (m MarketMaker) HouseCapsChanges(ctx context.Context, limit int) (json.RawMessage, error) {
	return m.do(ctx, http.MethodGet, m.Base+"/internal/house/caps/changes?limit="+strconv.Itoa(limit), nil, nil)
}

// HouseRooms returns what HOUSE may still buy and sell of a symbol (J0
// contract §4.2, unsigned); false while it does not quote it (404).
func (m MarketMaker) HouseRooms(ctx context.Context, symbol string) (ports.HouseRooms, bool, error) {
	raw, err := m.do(ctx, http.MethodGet, m.Base+"/internal/house/rooms/"+url.PathEscape(symbol), nil, nil)
	if apperr.Is(err, apperr.CodeNotFound) {
		return ports.HouseRooms{}, false, nil
	}
	if err != nil {
		return ports.HouseRooms{}, false, err
	}
	var body struct {
		Buy       decimal.Decimal `json:"buy"`
		Sell      decimal.Decimal `json:"sell"`
		UnitValue decimal.Decimal `json:"unit_value"`
		Inverse   bool            `json:"inverse"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return ports.HouseRooms{}, false, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "market-maker answered badly")
	}
	return ports.HouseRooms{Buy: body.Buy, Sell: body.Sell, UnitValue: body.UnitValue, Inverse: body.Inverse}, true, nil
}
