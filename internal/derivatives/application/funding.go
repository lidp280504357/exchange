package application

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	"github.com/skill/exchange/internal/derivatives/domain"
	"github.com/skill/exchange/internal/derivatives/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
)

// FundingGiveUp is how long a round waits for its rate before it is
// skipped (market-data-service had no samples for the period).
const FundingGiveUp = 2 * time.Hour

// fundingTime is the latest funding time at or before now.
func fundingTime(now time.Time, hours int32) time.Time {
	d := time.Duration(max(hours, 1)) * time.Hour
	return now.UTC().Truncate(d)
}

// SnapshotFunding takes, for each contract whose latest funding time has
// no round yet, the positions open now (plan §7.3 task 6: the positions
// held at the funding time). It holds the fill lock, so no fill is half
// applied. After an outage only the latest funding time gets a round; the
// ones missed are not charged.
func (s *Service) SnapshotFunding(ctx context.Context) (int, error) {
	contracts, err := s.Instruments.Contracts(ctx)
	if err != nil {
		return 0, err
	}
	s.fills.Lock()
	defer s.fills.Unlock()
	n := 0
	for _, c := range contracts {
		if c.Status == "DELISTED" {
			continue
		}
		at := fundingTime(s.Now(), c.FundingIntervalHours)
		err := s.Store.Tx(ctx, func(r ports.Repos) error {
			if round, err := r.Funding().Round(ctx, c.Symbol, at); err != nil || round != nil {
				return err
			}
			open, err := r.Positions().Open(ctx, c.Symbol)
			if err != nil {
				return err
			}
			payments := make([]domain.FundingPayment, 0, len(open))
			for _, p := range open {
				payments = append(payments, domain.FundingPayment{
					Symbol: c.Symbol, FundingTime: at, PositionID: p.ID, UserID: p.UserID, Side: p.Side, Qty: p.Qty, MarginMode: p.MarginMode,
				})
			}
			n++
			return r.Funding().Snapshot(ctx, domain.FundingRound{Symbol: c.Symbol, FundingTime: at, Positions: len(payments)}, payments)
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// SettleFunding settles the rounds whose rate market-data-service has
// settled: every position pays or receives |qty| x mark x |rate| at the
// settlement mark price (domain.FundingAmount), payers first, so
// FUNDING_CLEARING never runs short. Each payment is its own ledger step
// (key funding:<symbol>:<unix time>:<position>) under the owner's lock; a
// round interrupted midway goes on where it stopped.
func (s *Service) SettleFunding(ctx context.Context) (int, error) {
	rounds, err := s.Store.Read().Funding().Waiting(ctx)
	if err != nil {
		return 0, err
	}
	settled := 0
	for _, round := range rounds {
		if round.Rate.IsZero() && round.Mark.IsZero() {
			rate, mark, found, err := s.Rates.Rate(ctx, round.Symbol, round.FundingTime)
			if err != nil {
				return settled, err
			}
			if !found {
				if s.Now().Sub(round.FundingTime) > FundingGiveUp {
					s.Log.WarnContext(ctx, "funding round skipped: no settled rate", "symbol", round.Symbol, "funding_time", round.FundingTime)
					if err := s.Store.Tx(ctx, func(r ports.Repos) error {
						return r.Funding().Finish(ctx, round.Symbol, round.FundingTime, domain.FundingSkipped)
					}); err != nil {
						return settled, err
					}
				}
				continue
			}
			if err := s.Store.Tx(ctx, func(r ports.Repos) error {
				return r.Funding().SetRate(ctx, round.Symbol, round.FundingTime, rate, mark)
			}); err != nil {
				return settled, err
			}
			round.Rate, round.Mark = rate, mark
		}
		if err := s.settleRound(ctx, round); err != nil {
			return settled, err
		}
		settled++
	}
	return settled, nil
}

func (s *Service) settleRound(ctx context.Context, round domain.FundingRound) error {
	c, err := s.Instruments.Contract(ctx, round.Symbol)
	if err != nil {
		return err
	}
	payments, err := s.Store.Read().Funding().Unsettled(ctx, round.Symbol, round.FundingTime)
	if err != nil {
		return err
	}
	// Payers first.
	for _, pay := range []bool{true, false} {
		for _, p := range payments {
			amount := domain.FundingAmount(p.Qty, round.Mark, round.Rate, c.QuoteDecimals)
			if amount.IsNegative() != pay {
				continue
			}
			p.Rate, p.Mark = round.Rate, round.Mark
			if err := s.pay(ctx, c, p, amount); err != nil {
				return err
			}
		}
	}
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		return r.Funding().Finish(ctx, round.Symbol, round.FundingTime, domain.FundingSettled)
	})
}

// pay books one position's funding.
func (s *Service) pay(ctx context.Context, c domain.Contract, p domain.FundingPayment, amount decimal.Decimal) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, p.UserID); err != nil {
			return err
		}
		held, err := r.Positions().OfUser(ctx, p.UserID, p.Symbol)
		if err != nil {
			return err
		}
		var pos domain.Position
		for _, h := range held {
			if h.ID == p.PositionID {
				pos = h
			}
		}
		p.Amount, p.Insurance = amount, decimal.Zero
		if !amount.IsZero() {
			move := domain.FundingMove(pos, amount)
			outcomes, err := s.Ledger.Settle(ctx, ports.SettleRequest{
				IdemKey: fmt.Sprintf("funding:%s:%d:%s", p.Symbol, p.FundingTime.Unix(), p.PositionID),
				UserID:  p.UserID, Asset: c.Quote,
				Reference: fmt.Sprintf("%s funding %s", p.Symbol, p.FundingTime.Format(time.RFC3339)),
				Moves:     []domain.Move{move},
			})
			if err != nil {
				return err
			}
			p.Insurance = outcomes[0].Insurance
			if pos.ID != "" {
				pos = domain.ApplyFunding(pos, move, outcomes[0].User)
				pos.UpdatedAt = s.Now()
				if pos, err = r.Positions().Save(ctx, pos); err != nil {
					return err
				}
				if err := r.Emit(ctx, event.TopicDerivPosition, &derivativesv1.FundingPaid{
					Position: positionProto(pos), FundingTime: timestamppb.New(p.FundingTime), FundingRate: p.Rate.String(),
					MarkPrice: p.Mark.String(), Amount: amount.String(),
				}, "user", p.UserID); err != nil {
					return err
				}
			}
		}
		return r.Funding().Settle(ctx, p)
	})
}

// FundingPayments returns a page of the user's funding payments, newest
// first, and the cursor of the next page ("" on the last).
func (s *Service) FundingPayments(ctx context.Context, userID, symbol, cursor string, limit int) ([]domain.FundingPayment, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if cursor != "" {
		secs, id, ok := strings.Cut(cursor, ":")
		if _, err := strconv.ParseInt(secs, 10, 64); err != nil || !ok {
			return nil, "", apperr.Invalid("bad cursor")
		}
		if _, err := uuid.Parse(id); err != nil {
			return nil, "", apperr.Invalid("bad cursor")
		}
	}
	list, err := s.Store.Read().Funding().OfUser(ctx, userID, symbol, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	if len(list) <= limit {
		return list, "", nil
	}
	list = list[:limit]
	last := list[limit-1]
	return list, fmt.Sprintf("%d:%s", last.FundingTime.Unix(), last.PositionID), nil
}
