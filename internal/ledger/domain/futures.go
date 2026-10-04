package domain

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Perpetual contract settlement (requirements §11.7; plan §7.3 task 4).
// derivatives-service keeps the positions and works out what each step of
// a user's contract trading means for the user's FUTURES account in the
// settlement asset (USDT): it asks for a list of moves, and the ledger
// books them in order, one journal each, in one transaction. The moves
// that charge the user are capped at what the balance holds, so a trade
// the engine executed always settles: beyond it the insurance fund pays a
// loss or a funding payment, and a fee is waived.
//
// FUTURES frozen = the reservations of open orders + the margin of every
// position; unrealized profit is not booked. PNL_CLEARING is the
// counterparty of realized profit and loss and may go negative: with
// positions booked at their entry cost, PNL_CLEARING + Σ long cost −
// Σ short cost = 0 for every contract (invariant 6, checked by
// derivatives-service). FUNDING_CLEARING passes funding from payers to
// receivers and keeps the rounding.
const (
	// MoveUnfreeze: the user's FROZEN to AVAILABLE (reservations and margin
	// no longer needed).
	MoveUnfreeze = "UNFREEZE"
	// MoveFreeze: AVAILABLE to FROZEN (margin added).
	MoveFreeze = "FREEZE"
	// MoveFee: the user to FEE_REVENUE.
	MoveFee = "FEE"
	// MoveProfit: PNL_CLEARING to the user.
	MoveProfit = "PROFIT"
	// MoveLoss: the user to PNL_CLEARING.
	MoveLoss = "LOSS"
	// MoveFundingPay: the user to FUNDING_CLEARING.
	MoveFundingPay = "FUNDING_PAY"
	// MoveFundingReceive: FUNDING_CLEARING to the user.
	MoveFundingReceive = "FUNDING_RECEIVE"
	// MoveInsurance: the user to INSURANCE_FUND (the margin a liquidation
	// leaves above the bankruptcy price).
	MoveInsurance = "INSURANCE"
)

// AccountPnLClearing is the counterparty of realized profit and loss.
const AccountPnLClearing = "PNL_CLEARING"

// MaxFuturesMoves bounds a request.
const MaxFuturesMoves = 16

var moveTypes = []string{
	MoveUnfreeze, MoveFreeze, MoveFee, MoveProfit, MoveLoss, MoveFundingPay, MoveFundingReceive, MoveInsurance,
}

// FuturesMove is one step of a settlement.
type FuturesMove struct {
	Type   string
	Amount decimal.Decimal
	// Kind is the user's balance the move takes from or gives to:
	// AVAILABLE (the default) or FROZEN. FREEZE and UNFREEZE move between
	// the two.
	Kind string
	// Limit, when set, bounds what a FEE, LOSS or FUNDING_PAY takes from
	// the user, e.g. the margin of an isolated position.
	Limit *decimal.Decimal
	// Partial lets a FREEZE freeze what the available balance allows.
	Partial bool
	// EntryType books a PROFIT or LOSS as LIQUIDATION_SETTLE or ADL_SETTLE
	// instead of REALIZED_PNL.
	EntryType string
}

// FuturesRequest is one settlement step of a user.
type FuturesRequest struct {
	IdemKey string
	UserID  string
	Asset   string
	// Reference says what is settled, e.g. "BTC-USDT-PERP trade <id>".
	Reference string
	Moves     []FuturesMove
}

// FuturesOutcome is what a move did.
type FuturesOutcome struct {
	// JournalID is empty when nothing moved (a capped move on an empty
	// balance).
	JournalID string `json:"journal_id,omitempty"`
	// User is what the user's balance received (positive) or gave.
	User decimal.Decimal `json:"user"`
	// Insurance is what the insurance fund paid of a LOSS or FUNDING_PAY.
	Insurance decimal.Decimal `json:"insurance"`
	// Waived is the part of a FEE not charged or of a FREEZE not frozen.
	Waived decimal.Decimal `json:"waived"`
}

func (m FuturesMove) kind() string {
	if m.Kind == "" {
		return Available
	}
	return m.Kind
}

// Validate checks the request's shape; amounts against the asset's
// decimals are the caller's to check.
func (r FuturesRequest) Validate() error {
	switch {
	case r.IdemKey == "" || len(r.IdemKey) > 150:
		return apperr.Invalid("an idempotency key of at most 150 bytes is required")
	case r.Asset == "":
		return apperr.Invalid("the asset is required")
	case len(r.Reference) > 300:
		return apperr.Invalid("the reference is too long")
	case len(r.Moves) == 0 || len(r.Moves) > MaxFuturesMoves:
		return apperr.Invalid(fmt.Sprintf("1 to %d moves are required", MaxFuturesMoves))
	}
	if _, err := uuid.Parse(r.UserID); err != nil {
		return apperr.Invalid("user_id must be a UUID")
	}
	for i, m := range r.Moves {
		fail := func(msg string) error { return apperr.Invalid(fmt.Sprintf("move %d (%s): %s", i+1, m.Type, msg)) }
		capped := m.Type == MoveFee || m.Type == MoveLoss || m.Type == MoveFundingPay
		switch {
		case !slices.Contains(moveTypes, m.Type):
			return apperr.Invalid(fmt.Sprintf("move %d: unknown type %q", i+1, m.Type))
		case !m.Amount.IsPositive():
			return fail("the amount must be positive")
		case m.Kind != "" && m.Kind != Available && m.Kind != Frozen:
			return fail("the balance kind is AVAILABLE or FROZEN")
		case m.Limit != nil && (!capped || m.Limit.IsNegative()):
			return fail("only FEE, LOSS and FUNDING_PAY take a limit, at least 0")
		case m.Partial && m.Type != MoveFreeze:
			return fail("only FREEZE may be partial")
		case m.EntryType != "" && !pnlEntry(m):
			return fail("only PROFIT and LOSS take an entry type, REALIZED_PNL, LIQUIDATION_SETTLE or ADL_SETTLE")
		}
	}
	return nil
}

// pnlEntry reports whether m may carry its entry type.
func pnlEntry(m FuturesMove) bool {
	return (m.Type == MoveProfit || m.Type == MoveLoss) &&
		(m.EntryType == EntryRealizedPnL || m.EntryType == EntryLiquidationSettle || m.EntryType == EntryADLSettle)
}

// Hash digests the request, telling a replay from a different request
// under the same key.
func (r FuturesRequest) Hash() []byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s\n", r.IdemKey, r.UserID, r.Asset, r.Reference)
	for _, m := range r.Moves {
		limit := "-"
		if m.Limit != nil {
			limit = m.Limit.String()
		}
		fmt.Fprintf(h, "%s|%s|%s|%s|%t|%s\n", m.Type, m.Amount.String(), m.kind(), limit, m.Partial, m.EntryType)
	}
	return h.Sum(nil)
}

// FuturesAccounts returns the accounts a request may touch, in lock order.
func (r FuturesRequest) FuturesAccounts() []AccountKey {
	p := Posting{Lines: []Line{{Account: UserAccount(r.UserID, AccountFutures, r.Asset)}}}
	for _, m := range r.Moves {
		for _, t := range counterparties(m.Type) {
			p.Lines = append(p.Lines, Line{Account: SystemAccount(t, r.Asset)})
		}
	}
	return p.Accounts()
}

func counterparties(move string) []string {
	switch move {
	case MoveFee:
		return []string{AccountFeeRevenue}
	case MoveProfit:
		return []string{AccountPnLClearing}
	case MoveLoss:
		return []string{AccountPnLClearing, AccountInsuranceFund}
	case MoveFundingPay:
		return []string{AccountFundingClearing, AccountInsuranceFund}
	case MoveFundingReceive:
		return []string{AccountFundingClearing}
	case MoveInsurance:
		return []string{AccountInsuranceFund}
	}
	return nil
}

// FuturesPlan is a request worked out against the user's balance.
type FuturesPlan struct {
	Postings []Posting
	// Outcomes has one entry per move; Posted[i] is the index of move i's
	// journal in Postings, -1 when it moved nothing.
	Outcomes []FuturesOutcome
	Posted   []int
}

// FuturesPostings turns the request into its journals against the user's
// FUTURES account as it stands (user, locked by the caller), one per move
// that moves something, and the outcome of every move. Capped moves take
// min(amount, limit, balance); the balance follows the moves before them.
// System accounts are checked when the journals are posted: an insurance
// fund too small for a shortfall refuses the whole request.
func FuturesPostings(r FuturesRequest, user Account) (FuturesPlan, error) {
	if err := r.Validate(); err != nil {
		return FuturesPlan{}, err
	}
	u := UserAccount(r.UserID, AccountFutures, r.Asset)
	if user.Key != u {
		return FuturesPlan{}, fmt.Errorf("futures postings: account %s is not %s", user.Key, u)
	}
	balance := map[string]decimal.Decimal{Available: user.Available, Frozen: user.Frozen}
	sys := func(t string) AccountKey { return SystemAccount(t, r.Asset) }
	plan := FuturesPlan{Outcomes: make([]FuturesOutcome, len(r.Moves)), Posted: make([]int, len(r.Moves))}
	for i, m := range r.Moves {
		kind, a := m.kind(), m.Amount
		take := func() decimal.Decimal {
			t := decimal.Min(a, decimal.Max(balance[kind], decimal.Zero))
			if m.Limit != nil {
				t = decimal.Min(t, *m.Limit)
			}
			return t
		}
		var lines []Line
		var entry string
		out := FuturesOutcome{User: decimal.Zero, Insurance: decimal.Zero, Waived: decimal.Zero}
		switch m.Type {
		case MoveUnfreeze:
			entry = EntryOrderUnfreeze
			lines = []Line{{Account: u, Amount: a.Neg(), Kind: Frozen}, {Account: u, Amount: a, Kind: Available}}
			balance[Frozen], balance[Available] = balance[Frozen].Sub(a), balance[Available].Add(a)
		case MoveFreeze:
			entry = EntryOrderFreeze
			frozen := a
			if m.Partial {
				frozen = decimal.Min(a, decimal.Max(balance[Available], decimal.Zero))
			}
			out.Waived = a.Sub(frozen)
			if frozen.IsPositive() {
				lines = []Line{{Account: u, Amount: frozen.Neg(), Kind: Available}, {Account: u, Amount: frozen, Kind: Frozen}}
			}
			balance[Available], balance[Frozen] = balance[Available].Sub(frozen), balance[Frozen].Add(frozen)
		case MoveFee:
			entry = EntryTradeFee
			paid := take()
			out.User, out.Waived = paid.Neg(), a.Sub(paid)
			if paid.IsPositive() {
				lines = []Line{{Account: u, Amount: paid.Neg(), Kind: kind}, {Account: sys(AccountFeeRevenue), Amount: paid, Kind: Available}}
			}
			balance[kind] = balance[kind].Sub(paid)
		case MoveProfit, MoveFundingReceive, MoveLoss, MoveFundingPay:
			clearing := AccountPnLClearing
			entry = EntryRealizedPnL
			if m.Type == MoveFundingReceive || m.Type == MoveFundingPay {
				entry, clearing = EntryFundingPayment, AccountFundingClearing
			}
			if m.EntryType != "" {
				entry = m.EntryType
			}
			if m.Type == MoveProfit || m.Type == MoveFundingReceive {
				out.User = a
				lines = []Line{{Account: sys(clearing), Amount: a.Neg(), Kind: Available}, {Account: u, Amount: a, Kind: kind}}
				balance[kind] = balance[kind].Add(a)
				break
			}
			paid := take()
			out.User, out.Insurance = paid.Neg(), a.Sub(paid)
			if paid.IsPositive() {
				lines = append(lines, Line{Account: u, Amount: paid.Neg(), Kind: kind})
			}
			if out.Insurance.IsPositive() {
				lines = append(lines, Line{Account: sys(AccountInsuranceFund), Amount: out.Insurance.Neg(), Kind: Available})
			}
			lines = append(lines, Line{Account: sys(clearing), Amount: a, Kind: Available})
			balance[kind] = balance[kind].Sub(paid)
		case MoveInsurance:
			entry = EntryInsuranceContribution
			out.User = a.Neg()
			lines = []Line{{Account: u, Amount: a.Neg(), Kind: kind}, {Account: sys(AccountInsuranceFund), Amount: a, Kind: Available}}
			balance[kind] = balance[kind].Sub(a)
		}
		// Only the exact moves (UNFREEZE, FREEZE, INSURANCE) can overdraw.
		for _, k := range []string{Available, Frozen} {
			if balance[k].IsNegative() {
				return FuturesPlan{}, ErrInsufficientBalance.WithDetail("asset", r.Asset).WithDetail("account_type", AccountFutures).
					WithDetail("balance", fmt.Sprintf("%s (move %d, %s)", strings.ToLower(k), i+1, m.Type))
			}
		}
		plan.Outcomes[i], plan.Posted[i] = out, -1
		if len(lines) == 0 {
			continue
		}
		plan.Posted[i] = len(plan.Postings)
		plan.Postings = append(plan.Postings, Posting{
			IdemKey: fmt.Sprintf("futures:%s:%d", r.IdemKey, i), EntryType: entry,
			Memo: fmt.Sprintf("%s %s", r.Reference, m.Type), Lines: lines,
		})
	}
	return plan, nil
}

// FundInsurancePosting adds simulated funds to the insurance fund against
// ADJUSTMENT (INSURANCE_CONTRIBUTION): the test environment's seed of the
// fund, like the welcome credits of users.
func FundInsurancePosting(idemKey, asset string, amount decimal.Decimal, decimals int32, reason string) (Posting, error) {
	if err := checkAmount(amount, decimals); err != nil {
		return Posting{}, err
	}
	return Posting{IdemKey: idemKey, EntryType: EntryInsuranceContribution, Memo: reason, Lines: []Line{
		{Account: SystemAccount(AccountAdjustment, asset), Amount: amount.Neg(), Kind: Available},
		{Account: SystemAccount(AccountInsuranceFund, asset), Amount: amount, Kind: Available},
	}}, nil
}

// FuturesSettlement is a booked request: a repeat with the same key and
// content gets its outcomes back.
type FuturesSettlement struct {
	IdemKey     string
	UserID      string
	RequestHash []byte
	Reference   string
	Outcomes    []FuturesOutcome
	CreatedAt   time.Time
}
