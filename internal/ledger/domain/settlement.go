package domain

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/apperr"
)

// Trade statuses: settled, or parked after a refusal until an operator
// retries it.
const (
	TradeSettled = "SETTLED"
	TradeFailed  = "FAILED"
)

// Sides HOUSE takes in a trade against its reference liquidity (ADR-0015).
const (
	HouseBuy  = "BUY"
	HouseSell = "SELL"
)

// Trade is an engine trade to settle (trade.events: TradeExecuted), and the
// ledger's record of it.
type Trade struct {
	ID string
	// Number counts the symbol's trades from 1; 0 for trades from before
	// the engine numbered them.
	Number        uint64
	Symbol        string
	BaseAsset     string
	QuoteAsset    string
	Price         decimal.Decimal
	Quantity      decimal.Decimal
	Quote         decimal.Decimal
	BuyerOrderID  string
	BuyerUserID   string
	SellerOrderID string
	SellerUserID  string
	BuyerIsMaker  bool
	BuyerFee      decimal.Decimal // base
	SellerFee     decimal.Decimal // quote
	// BuyerLimit is the buy order's limit price, zero for a market buy.
	BuyerLimit decimal.Decimal
	// HouseSide is the side HOUSE took (HouseBuy, HouseSell), "" for a
	// trade between users. HOUSE's side settles on its MARKET_MAKER
	// system accounts, without an order, a freeze or a fee (ADR-0013).
	HouseSide string
	// BuyerAccount and SellerAccount are the accounts each side's order
	// traded from: SPOT (also ""), MARGIN_CROSS, or MARGIN_ISOLATED of the
	// trade's symbol (margin design §5.1); HOUSE's side has none.
	BuyerAccount  string
	SellerAccount string
	// BuyerAutoRepay and SellerAutoRepay: the side's order has side_effect
	// AUTO_REPAY, so what it receives repays its margin account's debt of
	// that asset, interest first (AutoRepayPostings).
	BuyerAutoRepay  bool
	SellerAutoRepay bool
	EventID         string
	ExecutedAt      time.Time

	Status    string
	ErrorCode string
	Error     string
	Attempts  int
	SettledAt time.Time
}

// ErrInvalidTrade reports a trade the ledger cannot settle as sent.
var ErrInvalidTrade = apperr.New(apperr.KindInvalid, "LEDGER_INVALID_TRADE", "the trade cannot be settled")

// Validate checks what settlement relies on: two different users, two
// different assets, positive amounts with quote = price x quantity, fees
// below what each side receives and a buy limit not below the price.
func (t Trade) Validate() error {
	fail := func(format string, args ...any) error {
		return ErrInvalidTrade.WithDetail("reason", fmt.Sprintf(format, args...))
	}
	switch {
	case t.ID == "" || t.Symbol == "":
		return fail("the trade lacks its ID or symbol")
	case t.BuyerUserID == "" || t.SellerUserID == "" || t.BuyerUserID == t.SellerUserID:
		return fail("a trade needs two different users")
	case t.BaseAsset == "" || t.QuoteAsset == "" || t.BaseAsset == t.QuoteAsset:
		return fail("a trade needs two different assets")
	case !t.Price.IsPositive() || !t.Quantity.IsPositive() || !t.Quote.Equal(t.Price.Mul(t.Quantity)):
		return fail("price %s x quantity %s is not the quote %s", t.Price, t.Quantity, t.Quote)
	case t.BuyerFee.IsNegative() || t.BuyerFee.GreaterThanOrEqual(t.Quantity):
		return fail("the buyer's fee %s is not below the quantity", t.BuyerFee)
	case t.SellerFee.IsNegative() || t.SellerFee.GreaterThanOrEqual(t.Quote):
		return fail("the seller's fee %s is not below the quote", t.SellerFee)
	case !t.BuyerLimit.IsZero() && t.BuyerLimit.LessThan(t.Price):
		return fail("the buyer's limit %s is below the price %s", t.BuyerLimit, t.Price)
	case t.HouseSide != "" && t.HouseSide != HouseBuy && t.HouseSide != HouseSell:
		return fail("HOUSE's side %q is neither BUY nor SELL", t.HouseSide)
	case t.HouseSide == HouseBuy && (!t.BuyerFee.IsZero() || !t.BuyerLimit.IsZero()):
		return fail("HOUSE buys without a fee or a limit")
	case t.HouseSide == HouseSell && !t.SellerFee.IsZero():
		return fail("HOUSE sells without a fee")
	case !tradeAccount(t.BuyerAccount) || !tradeAccount(t.SellerAccount):
		return fail("an order trades from SPOT, MARGIN_CROSS or MARGIN_ISOLATED, not %q/%q", t.BuyerAccount, t.SellerAccount)
	case t.BuyerAutoRepay && (t.HouseSide == HouseBuy || !MarginType(t.BuyerAccount)),
		t.SellerAutoRepay && (t.HouseSide == HouseSell || !MarginType(t.SellerAccount)):
		return fail("only an order on a margin account repays automatically")
	}
	return nil
}

func tradeAccount(a string) bool {
	return a == "" || a == AccountSpot || a == AccountMarginCross || a == AccountMarginIsolated
}

// account is the account a user's side of the trade settles on in asset:
// SPOT, or the margin account its order traded from (an isolated one is
// the trade's pair's).
func (t Trade) account(user, account, asset string) AccountKey {
	switch account {
	case AccountMarginCross:
		return AccountKey{OwnerType: OwnerUser, OwnerID: user, Type: AccountMarginCross, Asset: asset}
	case AccountMarginIsolated:
		return AccountKey{OwnerType: OwnerUser, OwnerID: user, Type: AccountMarginIsolated, Scope: t.Symbol, Asset: asset}
	}
	return UserAccount(user, AccountSpot, asset)
}

// onMargin reports whether a user's side of the trade is a margin
// account's.
func (t Trade) onMargin() bool {
	return (t.HouseSide != HouseBuy && MarginType(t.BuyerAccount)) || (t.HouseSide != HouseSell && MarginType(t.SellerAccount))
}

// repaySide is a side of the trade that repays automatically: the margin
// account and the asset it received, how much, and the journal's key.
type repaySide struct {
	ref      MarginRef
	asset    string
	received decimal.Decimal
	key      string
	order    string
}

func (t Trade) repaySides() []repaySide {
	var out []repaySide
	if t.BuyerAutoRepay {
		out = append(out, repaySide{
			ref: t.marginRef(t.BuyerUserID, t.BuyerAccount), asset: t.BaseAsset, received: t.Quantity.Sub(t.BuyerFee),
			key: "trade-repay:" + t.ID + ":buyer", order: t.BuyerOrderID,
		})
	}
	if t.SellerAutoRepay {
		out = append(out, repaySide{
			ref: t.marginRef(t.SellerUserID, t.SellerAccount), asset: t.QuoteAsset, received: t.Quote.Sub(t.SellerFee),
			key: "trade-repay:" + t.ID + ":seller", order: t.SellerOrderID,
		})
	}
	return out
}

func (t Trade) marginRef(user, account string) MarginRef {
	ref := MarginRef{UserID: user, AccountType: account}
	if account == AccountMarginIsolated {
		ref.Scope = t.Symbol
	}
	return ref
}

// RepayAccounts are the rows the trade's automatic repayments read and
// change: the debt and interest rows of what each such side received.
func (t Trade) RepayAccounts() []AccountKey {
	var out []AccountKey
	for _, r := range t.repaySides() {
		out = append(out, r.ref.DebtRow(r.asset), r.ref.InterestRow(r.asset))
	}
	return out
}

// AutoRepayPostings repays, for each side whose order has AUTO_REPAY, its
// margin account's debt of the asset the trade brought it — what it
// received less the fee, interest first, at most what is owed — in a
// MARGIN_REPAY journal of its own (key trade-repay:<trade>:<buyer|seller>),
// posted in the settlement's transaction; none when nothing is owed.
// accounts holds the debt and interest rows as locked (RepayAccounts).
func AutoRepayPostings(t Trade, accounts map[AccountKey]Account) []Posting {
	var out []Posting
	for _, r := range t.repaySides() {
		interest := decimal.Max(accounts[r.ref.InterestRow(r.asset)].Available.Neg(), decimal.Zero)
		principal := decimal.Max(accounts[r.ref.DebtRow(r.asset)].Available.Neg(), decimal.Zero)
		paid := decimal.Min(r.received, interest.Add(principal))
		if !paid.IsPositive() {
			continue
		}
		toInterest := decimal.Min(paid, interest)
		p := Posting{
			IdemKey: r.key, EntryType: EntryMarginRepay, SourceEventID: t.EventID,
			Memo:  fmt.Sprintf("auto-repay order %s trade %s %s", r.order, t.Symbol, t.ID),
			Lines: []Line{{Account: r.ref.Assets(r.asset), Amount: paid.Neg(), Kind: Available}},
		}
		if toInterest.IsPositive() {
			p.Lines = append(p.Lines, Line{Account: r.ref.InterestRow(r.asset), Amount: toInterest, Kind: Available})
		}
		if rest := paid.Sub(toInterest); rest.IsPositive() {
			p.Lines = append(p.Lines, Line{Account: r.ref.DebtRow(r.asset), Amount: rest, Kind: Available})
		}
		out = append(out, p)
	}
	return out
}

// Idempotency keys of a trade's journals.
func settleKey(id string) string  { return "trade:" + id }
func feeKey(id string) string     { return "trade-fee:" + id }
func releaseKey(id string) string { return "trade-release:" + id }

// SettlementPostings turns a trade into its journals (§11.1 step 7, §11.4):
//
//   - TRADE_SETTLE swaps the traded amounts: the buyer's frozen quote to
//     the seller's available quote, the seller's frozen base to the buyer's
//     available base. It carries nothing else, so its sums equal the
//     engine's traded amounts (invariant 5). Against HOUSE it is
//     HOUSE_TRADE_SETTLE: the user's side the same, HOUSE's side on the
//     available balances of its MARKET_MAKER accounts, which may go below
//     zero (ADR-0013). A side whose order traded from a margin account
//     settles on that account's asset rows, and the journal is
//     MARGIN_TRADE_SETTLE (margin design §3.2).
//   - TRADE_FEE moves each side's fee from what it received to FEE_REVENUE:
//     the buyer pays in base, the seller in quote (§11.3). No journal when
//     both fees are zero.
//   - ORDER_UNFREEZE gives a limit buy that traded below its limit the
//     difference (limit - price) x quantity back at once; the order froze
//     limit x quantity for it.
//
// The ledger posts them in one transaction.
func SettlementPostings(t Trade) ([]Posting, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	memo := fmt.Sprintf("trade %s %s", t.Symbol, t.ID)
	buyerBase, buyerQuote := t.account(t.BuyerUserID, t.BuyerAccount, t.BaseAsset), t.account(t.BuyerUserID, t.BuyerAccount, t.QuoteAsset)
	sellerBase, sellerQuote := t.account(t.SellerUserID, t.SellerAccount, t.BaseAsset), t.account(t.SellerUserID, t.SellerAccount, t.QuoteAsset)
	entry, buyerPays, sellerPays := EntryTradeSettle, Frozen, Frozen
	switch t.HouseSide {
	case HouseBuy:
		entry, buyerPays = EntryHouseTradeSettle, Available
		buyerBase, buyerQuote = SystemAccount(AccountMarketMaker, t.BaseAsset), SystemAccount(AccountMarketMaker, t.QuoteAsset)
	case HouseSell:
		entry, sellerPays = EntryHouseTradeSettle, Available
		sellerBase, sellerQuote = SystemAccount(AccountMarketMaker, t.BaseAsset), SystemAccount(AccountMarketMaker, t.QuoteAsset)
	}
	if t.onMargin() {
		entry = EntryMarginTradeSettle // a side's margin account: the same lines, its own entry type
	}
	out := []Posting{{IdemKey: settleKey(t.ID), EntryType: entry, SourceEventID: t.EventID, Memo: memo, Lines: []Line{
		{Account: buyerQuote, Amount: t.Quote.Neg(), Kind: buyerPays},
		{Account: sellerQuote, Amount: t.Quote, Kind: Available},
		{Account: sellerBase, Amount: t.Quantity.Neg(), Kind: sellerPays},
		{Account: buyerBase, Amount: t.Quantity, Kind: Available},
	}}}
	fee := Posting{IdemKey: feeKey(t.ID), EntryType: EntryTradeFee, SourceEventID: t.EventID, Memo: memo}
	if t.BuyerFee.IsPositive() {
		fee.Lines = append(fee.Lines,
			Line{Account: buyerBase, Amount: t.BuyerFee.Neg(), Kind: Available},
			Line{Account: SystemAccount(AccountFeeRevenue, t.BaseAsset), Amount: t.BuyerFee, Kind: Available})
	}
	if t.SellerFee.IsPositive() {
		fee.Lines = append(fee.Lines,
			Line{Account: sellerQuote, Amount: t.SellerFee.Neg(), Kind: Available},
			Line{Account: SystemAccount(AccountFeeRevenue, t.QuoteAsset), Amount: t.SellerFee, Kind: Available})
	}
	if len(fee.Lines) > 0 {
		out = append(out, fee)
	}
	if improvement := t.BuyerLimit.Sub(t.Price).Mul(t.Quantity); !t.BuyerLimit.IsZero() && improvement.IsPositive() {
		out = append(out, Posting{IdemKey: releaseKey(t.ID), EntryType: EntryOrderUnfreeze, SourceEventID: t.EventID, Memo: memo, Lines: []Line{
			{Account: buyerQuote, Amount: improvement.Neg(), Kind: Frozen},
			{Account: buyerQuote, Amount: improvement, Kind: Available},
		}})
	}
	for _, p := range out {
		if err := p.Validate(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Simulate applies the postings in order to copies of accounts and reports
// the first refusal (insufficient balance), so a trade is settled whole or
// not at all.
func Simulate(accounts []Account, postings []Posting) error {
	byKey := make(map[AccountKey]Account, len(accounts))
	for _, a := range accounts {
		byKey[a.Key] = a
	}
	for _, p := range postings {
		for _, l := range p.Lines {
			a, ok := byKey[l.Account]
			if !ok {
				return fmt.Errorf("simulate: account %s not loaded", l.Account)
			}
			if err := a.Apply(l); err != nil {
				return err
			}
			byKey[l.Account] = a
		}
	}
	return nil
}

// PostingAccounts returns the distinct accounts of postings in lock order.
func PostingAccounts(postings []Posting) []AccountKey {
	all := Posting{}
	for _, p := range postings {
		all.Lines = append(all.Lines, p.Lines...)
	}
	return all.Accounts()
}
