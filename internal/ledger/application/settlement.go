package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/ledger/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// SettleResult counts what a settlement pass did.
type SettleResult struct {
	Settled, Failed, Skipped int
	// Refused holds the trades parked as FAILED in this pass.
	Refused []domain.Trade
}

// Settle books engine trades in one transaction (§11.1 step 7): per trade
// TRADE_SETTLE, TRADE_FEE and, for a limit buy that traded below its
// limit, ORDER_UNFREEZE (domain.SettlementPostings). Trades already
// recorded are skipped, so redeliveries are harmless. A trade the ledger
// refuses (invalid, too many decimals, insufficient frozen funds) is
// recorded as FAILED with the reason and nothing of it is posted, while
// the other trades go on; reconciliation reports it and RetryFailed
// settles it once the cause is fixed. Other errors (database,
// instrument-service) fail the whole call for the consumer to retry.
func (s *Service) Settle(ctx context.Context, trades []domain.Trade) (SettleResult, error) {
	return s.settle(ctx, trades, false)
}

// RetryFailed settles up to limit FAILED trades again, oldest first.
func (s *Service) RetryFailed(ctx context.Context, limit int) (SettleResult, error) {
	failed, err := s.Store.Read().Trades().List(ctx, domain.TradeFailed, limit)
	if err != nil {
		return SettleResult{}, err
	}
	for i, j := 0, len(failed)-1; i < j; i, j = i+1, j-1 {
		failed[i], failed[j] = failed[j], failed[i]
	}
	return s.settle(ctx, failed, true)
}

// HouseParked is what HOUSE owes, by asset, in the trades parked as FAILED
// (the base it sold, the quote it paid): not off its MARKET_MAKER
// accounts yet, so its liquidity publisher takes it off the holdings it
// quotes from until the retry settles them (review L2, 2026-10-02).
func (s *Service) HouseParked(ctx context.Context) (map[string]decimal.Decimal, error) {
	failed, err := s.Store.Read().Trades().List(ctx, domain.TradeFailed, 1000)
	if err != nil {
		return nil, err
	}
	out := map[string]decimal.Decimal{}
	for _, t := range failed {
		switch t.HouseSide {
		case domain.HouseSell:
			out[t.BaseAsset] = out[t.BaseAsset].Add(t.Quantity)
		case domain.HouseBuy:
			out[t.QuoteAsset] = out[t.QuoteAsset].Add(t.Quote)
		}
	}
	return out, nil
}

// Trades lists recorded trades, newest first, optionally of one status.
func (s *Service) Trades(ctx context.Context, status string, limit int) ([]domain.Trade, error) {
	if status != "" && status != domain.TradeSettled && status != domain.TradeFailed {
		return nil, apperr.Invalid("status must be SETTLED or FAILED")
	}
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	return s.Store.Read().Trades().List(ctx, status, limit)
}

type plannedTrade struct {
	trade    domain.Trade
	postings []domain.Posting
	refusal  error
}

func (s *Service) settle(ctx context.Context, trades []domain.Trade, retry bool) (SettleResult, error) {
	var res SettleResult
	if len(trades) == 0 {
		return res, nil
	}
	// Journals and precision are worked out before the transaction; only
	// balances depend on it.
	plans := make([]plannedTrade, 0, len(trades))
	for _, t := range trades {
		p := plannedTrade{trade: t}
		p.postings, p.refusal = domain.SettlementPostings(t)
		for i := 0; p.refusal == nil && i < len(p.postings); i++ {
			p.refusal = s.checkPrecision(ctx, p.postings[i])
		}
		if p.refusal != nil && !refused(p.refusal) {
			return SettleResult{}, p.refusal
		}
		plans = append(plans, p)
	}
	ids := make([]string, len(plans))
	for i, p := range plans {
		ids[i] = p.trade.ID
	}
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		res = SettleResult{}
		statuses, err := r.Trades().Statuses(ctx, ids)
		if err != nil {
			return err
		}
		// Every account of the batch is locked up front in one order, so
		// concurrent postings cannot deadlock with this transaction.
		var all []domain.Posting
		for _, p := range plans {
			all = append(all, p.postings...)
		}
		if len(all) > 0 {
			if _, err := r.Accounts().Lock(ctx, domain.PostingAccounts(all)); err != nil {
				return err
			}
		}
		for _, p := range plans {
			status, known := statuses[p.trade.ID]
			if !due(status, known, retry) {
				res.Skipped++
				continue
			}
			t, err := s.settleOne(ctx, r, p)
			if err != nil {
				return err
			}
			if known {
				t.Attempts++
				err = r.Trades().Update(ctx, t)
			} else {
				t.Attempts = 1
				err = r.Trades().Insert(ctx, t)
			}
			if err != nil {
				return err
			}
			statuses[t.ID] = t.Status
			if t.Status == domain.TradeSettled {
				res.Settled++
			} else {
				res.Failed++
				res.Refused = append(res.Refused, t)
			}
		}
		return nil
	})
	return res, err
}

// due reports whether a pass handles a trade: a first pass the trades not
// recorded yet (a duplicate in the batch is recorded by then), a retry the
// ones parked as FAILED.
func due(status string, known, retry bool) bool {
	if retry {
		return known && status == domain.TradeFailed
	}
	return !known
}

// settleOne posts one planned trade inside r's transaction, or returns it
// marked FAILED when the ledger refuses it; then nothing of it is written.
func (s *Service) settleOne(ctx context.Context, r ports.Repos, p plannedTrade) (domain.Trade, error) {
	t := p.trade
	if p.refusal == nil {
		accounts, err := r.Accounts().Lock(ctx, domain.PostingAccounts(p.postings))
		if err != nil {
			return t, err
		}
		p.refusal = domain.Simulate(accounts, p.postings)
	}
	if p.refusal != nil {
		if !refused(p.refusal) {
			return t, p.refusal
		}
		e := apperr.From(p.refusal)
		t.Status, t.ErrorCode, t.Error = domain.TradeFailed, e.Code, refusalText(e)
		return t, nil
	}
	for _, posting := range p.postings {
		if _, err := s.post(ctx, r, posting); err != nil {
			// The simulation passed, so this is not a balance refusal.
			return t, fmt.Errorf("settle trade %s: %w", t.ID, err)
		}
	}
	t.Status, t.ErrorCode, t.Error, t.SettledAt = domain.TradeSettled, "", "", s.Now()
	return t, nil
}

// refused tells a business refusal, which parks the trade, from a failure
// worth retrying.
func refused(err error) bool {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Kind {
	case apperr.KindInvalid, apperr.KindNotFound, apperr.KindConflict, apperr.KindUnprocessable:
		return true
	}
	return false
}

func refusalText(e *apperr.Error) string {
	msg := e.Message
	for _, k := range []string{"reason", "asset", "account_type", "balance", "decimals"} {
		if v, ok := e.Details[k]; ok {
			msg += fmt.Sprintf(" %s=%v", k, v)
		}
	}
	return msg
}
