package application

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// A user's money on their page (design 2026-10-02 §4.1): balances valued
// in USDT, holds on part of a SPOT balance, single orders canceled and
// positions closed at the market. The ledger audits holds itself; the
// rest is audited here.

// BalanceRow is one of a user's balances with its worth.
type BalanceRow struct {
	ports.Balance
	Total decimal.Decimal
	// ValueUSDT is nil when the asset has no USDT price.
	ValueUSDT *decimal.Decimal
}

// UserBalances is a user's balances, SPOT first, the larger worth first,
// with their sum in USDT and the assets that have no price.
type UserBalances struct {
	Balances  []BalanceRow
	TotalUSDT decimal.Decimal
	Unpriced  []string
}

// valuer returns a function valuing an amount of an asset in USDT at its
// USDT pair's last price (false without one, or with one older than a
// positive maxAge); prices are read once.
func (s *Service) valuer(ctx context.Context, maxAge time.Duration) func(asset string, amount decimal.Decimal) (decimal.Decimal, bool) {
	var prices ports.Prices
	if s.Prices != nil {
		var err error
		if prices, err = s.Prices.Prices(ctx, maxAge); err != nil {
			s.Log.WarnContext(ctx, "no prices to value with", "error", err)
		}
	}
	return func(asset string, amount decimal.Decimal) (decimal.Decimal, bool) {
		if asset == "USDT" {
			return amount, true
		}
		px, ok := prices[asset+"-USDT"]
		if !ok {
			return decimal.Zero, false
		}
		return amount.Mul(px).Round(2), true
	}
}

// Balances returns a user's balances valued in USDT.
func (s *Service) Balances(ctx context.Context, p Principal, userID string) (UserBalances, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return UserBalances{}, err
	}
	if err := needUser(userID); err != nil {
		return UserBalances{}, err
	}
	list, err := s.Users.Balances(ctx, userID)
	if err != nil {
		return UserBalances{}, err
	}
	value := s.valuer(ctx, 0)
	out := UserBalances{Balances: make([]BalanceRow, 0, len(list)), Unpriced: []string{}}
	unpriced := map[string]bool{}
	for _, b := range list {
		available, err1 := decimal.NewFromString(b.Available)
		frozen, err2 := decimal.NewFromString(b.Frozen)
		if err1 != nil || err2 != nil {
			return UserBalances{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "ledger-service answered badly")
		}
		row := BalanceRow{Balance: b, Total: available.Add(frozen)}
		if v, ok := value(b.Asset, row.Total); ok {
			row.ValueUSDT = &v
			out.TotalUSDT = out.TotalUSDT.Add(v)
		} else if row.Total.IsPositive() && !unpriced[b.Asset] {
			unpriced[b.Asset] = true
			out.Unpriced = append(out.Unpriced, b.Asset)
		}
		out.Balances = append(out.Balances, row)
	}
	worth := func(r BalanceRow) decimal.Decimal {
		if r.ValueUSDT == nil {
			return decimal.NewFromInt(-1)
		}
		return *r.ValueUSDT
	}
	sort.SliceStable(out.Balances, func(i, j int) bool {
		a, b := out.Balances[i], out.Balances[j]
		if a.AccountType != b.AccountType {
			return a.AccountType == AccountSpot
		}
		if c := worth(a).Cmp(worth(b)); c != 0 {
			return c > 0
		}
		return a.Asset < b.Asset
	})
	sort.Strings(out.Unpriced)
	return out, nil
}

// Holds lists a user's holds, newest first.
func (s *Service) Holds(ctx context.Context, p Principal, userID string) ([]ports.Hold, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, err
	}
	if err := needUser(userID); err != nil {
		return nil, err
	}
	return s.Ledger.Holds(ctx, userID, false)
}

// PlaceHold freezes amount of a user's SPOT balance; the ledger books it
// (ADMIN_FREEZE) and audits it as ledger.hold_placed. The hold's ID comes
// from the request's key, so the same request again finds the same hold.
func (s *Service) PlaceHold(ctx context.Context, p Principal, key, userID, asset string, amount decimal.Decimal, reason string) (ports.Hold, error) {
	if err := p.require(domain.PermLedgerHold); err != nil {
		return ports.Hold{}, err
	}
	if err := needUser(userID); err != nil {
		return ports.Hold{}, err
	}
	if err := needReason(reason); err != nil {
		return ports.Hold{}, err
	}
	asset = strings.ToUpper(strings.TrimSpace(asset))
	if asset == "" || !amount.IsPositive() {
		return ports.Hold{}, apperr.Invalid("an asset and a positive amount are required")
	}
	reason = strings.TrimSpace(reason)
	c, err := s.claimKey(ctx, p, key, scopeHold, fingerprint(userID, asset, amount.String(), reason))
	if err != nil {
		return ports.Hold{}, err
	}
	return s.Ledger.PlaceHold(ctx, c.Ref, userID, asset, amount, p.Admin.Email, reason)
}

// ReleaseHold returns one of a user's holds to their available balance;
// the ledger books it (ADMIN_UNFREEZE) and audits it as
// ledger.hold_released. The same request again with the same key answers
// with the hold it released.
func (s *Service) ReleaseHold(ctx context.Context, p Principal, key, userID, holdID, reason string) (ports.Hold, error) {
	if err := p.require(domain.PermLedgerHold); err != nil {
		return ports.Hold{}, err
	}
	if err := needUser(userID); err != nil {
		return ports.Hold{}, err
	}
	if err := needReason(reason); err != nil {
		return ports.Hold{}, err
	}
	reason = strings.TrimSpace(reason)
	c, err := s.claimKey(ctx, p, key, scopeRelease, fingerprint(userID, holdID, reason))
	if err != nil {
		return ports.Hold{}, err
	}
	holds, err := s.Ledger.Holds(ctx, userID, false)
	if err != nil {
		return ports.Hold{}, err
	}
	for _, h := range holds {
		if h.ID != holdID {
			continue
		}
		if !c.Fresh && !h.ReleasedAt.IsZero() && h.ReleasedBy == p.Admin.Email && h.ReleaseReason == reason {
			return h, nil
		}
		return s.Ledger.ReleaseHold(ctx, holdID, p.Admin.Email, reason)
	}
	return ports.Hold{}, apperr.NotFound("no such hold")
}

// orderID reads the order ID of an order as a service renders it.
func orderID(raw json.RawMessage) string {
	var o struct {
		OrderID string `json:"order_id"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.OrderID
}

func (s *Service) orderAction(p Principal, userID, id, reason string) error {
	if err := p.require(domain.PermOrdersCancel); err != nil {
		return err
	}
	if err := needUser(userID); err != nil {
		return err
	}
	if err := needReason(reason); err != nil {
		return err
	}
	if _, err := uuid.Parse(id); err != nil {
		return apperr.NotFound("no such order")
	}
	return nil
}

// CancelOrder asks the engine to cancel one of a user's spot orders,
// audited as admin.orders.canceled.
func (s *Service) CancelOrder(ctx context.Context, p Principal, userID, id, reason string) (json.RawMessage, error) {
	if err := s.orderAction(p, userID, id, reason); err != nil {
		return nil, err
	}
	raw, err := s.Orders.Cancel(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]string{"order_id": id})
	return raw, s.audit(ctx, p, "user:"+userID, "admin.orders.canceled", reason, string(details))
}

// ContractOrders returns a user's active contract orders.
func (s *Service) ContractOrders(ctx context.Context, p Principal, userID string) (json.RawMessage, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, err
	}
	if err := needUser(userID); err != nil {
		return nil, err
	}
	return s.Derivatives.OpenOrders(ctx, userID)
}

// CancelContractOrder asks the engine to cancel one of a user's contract
// orders, audited as admin.derivatives.order_canceled.
func (s *Service) CancelContractOrder(ctx context.Context, p Principal, userID, id, reason string) (json.RawMessage, error) {
	if err := s.orderAction(p, userID, id, reason); err != nil {
		return nil, err
	}
	raw, err := s.Derivatives.CancelOrder(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]string{"order_id": id})
	return raw, s.audit(ctx, p, "user:"+userID, "admin.derivatives.order_canceled", reason, string(details))
}

// FuturesMargin measures a user's cross margin account and what a debit of
// its FUTURES balance would leave: a negative adjustment lowers the equity
// at once, and the next round of the margin monitor may liquidate it, so
// the confirmation shows it (C5.5 ⑧).
func (s *Service) FuturesMargin(ctx context.Context, p Principal, userID string, debit decimal.Decimal) (json.RawMessage, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, err
	}
	if err := needUser(userID); err != nil {
		return nil, err
	}
	if debit.IsNegative() {
		return nil, apperr.Invalid("the debit is a positive amount")
	}
	return s.Derivatives.CrossMargin(ctx, userID, debit)
}

// Positions returns a user's open contract positions.
func (s *Service) Positions(ctx context.Context, p Principal, userID string) (json.RawMessage, error) {
	if err := p.require(domain.PermUsersRead); err != nil {
		return nil, err
	}
	if err := needUser(userID); err != nil {
		return nil, err
	}
	return s.Derivatives.Positions(ctx, userID)
}

// closeAttempts and closeWait bound how long ClosePosition waits for the
// engine to confirm the cancels of the user's orders on the contract, and
// then for its order's outcome.
const closeAttempts = 8

// ClosePosition closes a user's position at the market: the user's
// orders resting on the contract come off, then a reduce-only market
// order of kind ADMIN takes the position. The request is audited as
// admin.derivatives.position_close_requested with the claim of its key
// (once, whatever the retries), its outcome as
// admin.derivatives.position_closed once the order is final, with what
// filled (C5.5 ⑧: on a thin book it may fill in part, the rest of the
// position stays). The order's client ID comes from the request's key, so
// the same request again finds the same order. The answer is the order as
// last seen.
func (s *Service) ClosePosition(ctx context.Context, p Principal, key, userID, symbol, side, reason string) (json.RawMessage, error) {
	if err := p.require(domain.PermDerivativesEdit); err != nil {
		return nil, err
	}
	if err := needUser(userID); err != nil {
		return nil, err
	}
	if err := needReason(reason); err != nil {
		return nil, err
	}
	symbol, side = strings.ToUpper(strings.TrimSpace(symbol)), strings.ToUpper(strings.TrimSpace(side))
	if symbol == "" {
		return nil, apperr.Invalid("the symbol is required")
	}
	reason = strings.TrimSpace(reason)
	var c claim
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		// The reason is not bound: confirming again with other words looks
		// the same close up (C5.5 ⑱).
		if c, err = s.claimIn(ctx, r, p, key, scopeClose, fingerprint(userID, symbol, side)); err != nil || !c.Fresh {
			return err
		}
		details, _ := json.Marshal(map[string]string{"symbol": symbol, "position_side": side, "client_order_id": c.Ref})
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "user:" + userID, Action: "admin.derivatives.position_close_requested", Actor: p.Admin.Email, Reason: reason,
			Details: string(details),
		}, p.Admin.Email)
	})
	if err != nil {
		return nil, err
	}
	wait := s.CloseWait
	if wait <= 0 {
		wait = 700 * time.Millisecond
	}
	pause := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
			return nil
		}
	}
	var raw json.RawMessage
	for attempt := 1; ; attempt++ {
		var err error
		raw, err = s.Derivatives.ClosePosition(ctx, userID, symbol, side, c.Ref)
		if err == nil {
			break
		}
		if apperr.From(err).Code != "DERIV_CLOSE_PENDING" || attempt >= closeAttempts {
			return nil, err
		}
		if err := pause(); err != nil {
			return nil, err
		}
	}
	// The market order is final within moments (filled, or what the book
	// held filled and the rest canceled); until then its outcome is not
	// audited.
	id := orderID(raw)
	for attempt := 1; ; attempt++ {
		o := closeOrderOf(raw)
		if o.final() {
			details, _ := json.Marshal(map[string]any{
				"symbol": symbol, "position_side": side, "order_id": id, "status": o.Status, "quantity": o.Quantity,
				"filled_quantity": o.Filled, "complete": o.Status == "FILLED", "repeated": !c.Fresh,
			})
			return raw, s.audit(ctx, p, "user:"+userID, "admin.derivatives.position_closed", reason, string(details))
		}
		if attempt >= closeAttempts || id == "" {
			return raw, nil
		}
		if err := pause(); err != nil {
			return nil, err
		}
		if next, err := s.Derivatives.Order(ctx, userID, id); err == nil {
			raw = next
		}
	}
}

// closeOrder is what ClosePosition reads of its order.
type closeOrder struct {
	Status   string `json:"status"`
	Quantity string `json:"quantity"`
	Filled   string `json:"filled_quantity"`
}

func closeOrderOf(raw json.RawMessage) closeOrder {
	var o closeOrder
	_ = json.Unmarshal(raw, &o)
	return o
}

// final reports whether the order is done: filled, or ended with what it
// filled.
func (o closeOrder) final() bool {
	switch o.Status {
	case "FILLED", "CANCELED", "REJECTED", "EXPIRED":
		return true
	}
	return false
}
