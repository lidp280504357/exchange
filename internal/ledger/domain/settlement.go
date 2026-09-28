package domain

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Trade statuses: settled, or parked after a refusal until an operator
// retries it.
const (
	TradeSettled = "SETTLED"
	TradeFailed  = "FAILED"
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
	EventID    string
	ExecutedAt time.Time

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
	}
	return nil
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
//     engine's traded amounts (invariant 5).
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
	buyerBase, buyerQuote := UserAccount(t.BuyerUserID, AccountSpot, t.BaseAsset), UserAccount(t.BuyerUserID, AccountSpot, t.QuoteAsset)
	sellerBase, sellerQuote := UserAccount(t.SellerUserID, AccountSpot, t.BaseAsset), UserAccount(t.SellerUserID, AccountSpot, t.QuoteAsset)
	out := []Posting{{IdemKey: settleKey(t.ID), EntryType: EntryTradeSettle, SourceEventID: t.EventID, Memo: memo, Lines: []Line{
		{Account: buyerQuote, Amount: t.Quote.Neg(), Kind: Frozen},
		{Account: sellerQuote, Amount: t.Quote, Kind: Available},
		{Account: sellerBase, Amount: t.Quantity.Neg(), Kind: Frozen},
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
