package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/ledger/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pg"
)

// MarginResult is a booked margin request: one journal per move.
type MarginResult struct {
	Journals []string
	Replayed bool
}

// PostMargin books a margin-service operation (margin design §3.2): the
// moves in order, one journal each (domain.MarginPostings), and the
// request in margin_postings, all in one transaction. The same key with
// the same request returns the first journals, with another request
// COMMON_IDEMPOTENCY_CONFLICT; a refusal (an asset row overdrawn, a debt
// overpaid, a transfer of what a debt holds) books nothing.
func (s *Service) PostMargin(ctx context.Context, req domain.MarginRequest) (MarginResult, error) {
	if err := req.Validate(); err != nil {
		return MarginResult{}, err
	}
	for _, asset := range req.Assets() {
		decimals, err := s.Assets.Decimals(ctx, asset)
		if err != nil {
			return MarginResult{}, err
		}
		for i, m := range req.Moves {
			if m.Asset == asset && (!fits(m.Amount, decimals) || !fits(m.Interest, decimals)) {
				return MarginResult{}, apperr.New(apperr.KindInvalid, "LEDGER_AMOUNT_PRECISION",
					fmt.Sprintf("move %d has more than %d decimals", i+1, decimals)).WithDetail("asset", asset).WithDetail("decimals", decimals)
			}
		}
	}
	hash := req.Hash()
	var res MarginResult
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		res, err = s.postMargin(ctx, r, req, hash)
		return err
	})
	if c, ok := pg.UniqueViolation(err); ok && c == "margin_postings_pkey" {
		// A concurrent request with the same key won: report it.
		done, err := s.Store.Read().Margins().ByKey(ctx, req.IdemKey)
		if err != nil {
			return MarginResult{}, err
		}
		if done == nil {
			return MarginResult{}, errors.New("margin posting vanished after a key conflict")
		}
		if !bytes.Equal(done.RequestHash, hash) {
			return MarginResult{}, domain.ErrIdempotencyConflict
		}
		return MarginResult{Journals: done.Journals, Replayed: true}, nil
	}
	return res, err
}

func (s *Service) postMargin(ctx context.Context, r ports.Repos, req domain.MarginRequest, hash []byte) (MarginResult, error) {
	replay := func() (MarginResult, bool, error) {
		done, err := r.Margins().ByKey(ctx, req.IdemKey)
		if err != nil || done == nil {
			return MarginResult{}, false, err
		}
		if !bytes.Equal(done.RequestHash, hash) {
			return MarginResult{}, true, domain.ErrIdempotencyConflict
		}
		return MarginResult{Journals: done.Journals, Replayed: true}, true, nil
	}
	if res, ok, err := replay(); ok || err != nil {
		return res, err
	}
	accounts, err := r.Accounts().Lock(ctx, req.MarginAccounts())
	if err != nil {
		return MarginResult{}, err
	}
	// A repeat that waited for the accounts behind the first request finds
	// it booked now: it replays rather than moving the balances the first
	// one left (review CJ).
	if res, ok, err := replay(); ok || err != nil {
		return res, err
	}
	postings, err := domain.MarginPostings(req, accounts)
	if err != nil {
		return MarginResult{}, err
	}
	journals := make([]string, len(postings))
	for i, p := range postings {
		posted, err := s.post(ctx, r, p)
		if err != nil {
			return MarginResult{}, err
		}
		journals[i] = posted.JournalID
	}
	if err := r.Margins().Insert(ctx, domain.MarginPosting{
		IdemKey: req.IdemKey, UserID: req.Account.UserID, RequestHash: hash, Reference: req.Reference, Journals: journals, CreatedAt: s.Now(),
	}); err != nil {
		return MarginResult{}, err
	}
	return MarginResult{Journals: journals}, nil
}

// AccrueInterest books an hour's interest of one asset on many margin
// accounts in one MARGIN_INTEREST journal (margin design §4.3, key
// margin-interest:<key>): each account's interest row -amount, HOUSE's
// MARGIN_INTEREST_INCOME the sum. A repeat replays the journal.
func (s *Service) AccrueInterest(ctx context.Context, req domain.InterestRequest) (Result, error) {
	p, err := req.Posting()
	if err != nil {
		return Result{}, err
	}
	return s.Post(ctx, p)
}

// MarginBalances lists the rows of a user's margin accounts: the assets,
// the debts and the interest, per account, scope and asset.
func (s *Service) MarginBalances(ctx context.Context, userID string) ([]domain.Account, error) {
	if _, err := domain.ParseMarginRef(userID, domain.AccountMarginCross, ""); err != nil {
		return nil, err
	}
	return s.Store.Read().Accounts().Margin(ctx, userID)
}

// MarginDebts lists every margin debt and interest row that owes
// something, for margin-service's reconciliation (invariant 7).
func (s *Service) MarginDebts(ctx context.Context) ([]domain.Account, error) {
	return s.Store.Read().Accounts().MarginDebts(ctx)
}

// FreezeScoped is Freeze on any account orders freeze: SPOT, FUTURES or a
// margin account's asset row (MARGIN_CROSS, or MARGIN_ISOLATED with its
// pair as scope).
func (s *Service) FreezeScoped(ctx context.Context, idemKey, entryType, userID, accountType, scope, asset string, amount decimal.Decimal,
	reference string,
) (Result, error) {
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return Result{}, err
	}
	p, err := domain.FreezeScopedPosting(scoped(userID, idemKey), entryType, userID, accountType, scope, asset, amount, decimals, reference)
	if err != nil {
		return Result{}, err
	}
	return s.Post(ctx, p)
}

// ErrTradesUnsettled answers RepayReleased while the order's trades are
// not all settled (B163): what their settlement gives back is not in the
// account yet. The caller tries again.
var ErrTradesUnsettled = apperr.New(apperr.KindUnavailable, "LEDGER_TRADES_UNSETTLED", "the order's trades are not all settled yet; try again")

// RepayReleased repays, as an order on a margin account that borrowed
// for its freeze ends (B160), up to upTo of the account's debt of asset
// from its available balance, interest first (domain.RepayReleasedPosting),
// once per order: a repeat returns the first journal. Only once the
// order's settled trades come to filled, the quantity it filled (B163):
// before, ErrTradesUnsettled (a zero filled waits for nothing). The zero
// Result and a zero amount when nothing was owed or available (nothing is
// kept then, so a later call may still repay).
func (s *Service) RepayReleased(ctx context.Context, orderID, userID, accountType, scope, asset string, upTo, filled decimal.Decimal) (Result, decimal.Decimal, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return Result{}, decimal.Zero, apperr.Invalid("order_id must be a UUID")
	}
	ref, err := domain.ParseMarginRef(userID, accountType, scope)
	if err != nil {
		return Result{}, decimal.Zero, err
	}
	if !upTo.IsPositive() {
		return Result{}, decimal.Zero, apperr.Invalid("up_to must be positive")
	}
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return Result{}, decimal.Zero, err
	}
	// A fill's quote may carry more decimals than the asset (tick x lot).
	if upTo = upTo.Truncate(decimals); !upTo.IsPositive() {
		return Result{}, decimal.Zero, nil
	}
	var res Result
	repaid := decimal.Zero
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		res, repaid = Result{}, decimal.Zero
		if j, err := r.Journals().ByIdemKey(ctx, domain.ReleaseRepayKey(orderID)); err != nil || j != nil {
			if j != nil {
				res = Result{JournalID: j.ID, Seq: j.Seq, Replayed: true}
			}
			return err
		}
		if filled.IsPositive() {
			settled, err := r.Trades().SettledOfMarginOrder(ctx, orderID)
			if err != nil {
				return err
			}
			if settled.LessThan(filled) {
				return ErrTradesUnsettled.WithDetail("settled", settled.String()).WithDetail("filled", filled.String())
			}
		}
		keys := []domain.AccountKey{ref.Assets(asset), ref.DebtRow(asset), ref.InterestRow(asset)}
		accounts, err := r.Accounts().Lock(ctx, keys)
		if err != nil {
			return err
		}
		byKey := make(map[domain.AccountKey]domain.Account, len(accounts))
		for _, a := range accounts {
			byKey[a.Key] = a
		}
		p, ok := domain.RepayReleasedPosting(ref, asset, orderID, upTo, byKey)
		if !ok {
			return nil
		}
		if res, err = s.post(ctx, r, p); err != nil {
			return err
		}
		repaid = p.Lines[0].Amount.Neg()
		return nil
	})
	return res, repaid, err
}

// UnfreezeScoped is Unfreeze on the accounts FreezeScoped freezes.
func (s *Service) UnfreezeScoped(ctx context.Context, idemKey, entryType, userID, accountType, scope, asset string, amount decimal.Decimal,
	reference string,
) (Result, error) {
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return Result{}, err
	}
	p, err := domain.UnfreezeScopedPosting(scoped(userID, idemKey), entryType, userID, accountType, scope, asset, amount, decimals, reference)
	if err != nil {
		return Result{}, err
	}
	return s.Post(ctx, p)
}
