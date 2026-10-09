package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
)

// BorrowInput is a request to borrow.
type BorrowInput struct {
	UserID  string
	IdemKey string
	Account domain.Account
	Asset   string
	Amount  decimal.Decimal
	// OrderID is set when an order borrows what it lacks (AUTO_BORROW).
	OrderID string
}

func (in BorrowInput) hash() []byte {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%s", in.Account.Key(), in.Asset, in.Amount.String(), in.OrderID))
	return sum[:]
}

// internalKeys start the keys margin-service gives its own writes: an
// order's borrow, an automatic repayment, a liquidation's writes, a
// purge's repayments (Settle).
var internalKeys = []string{"order:", AutoRepayPrefix, "liquidation:", SettlePrefix}

// checkKey validates a client's Idempotency-Key, which may not take an
// internal key's form (review CR: a client's "trade-repay:..." would hide
// the automatic repayment of that key).
func checkKey(key string) error {
	if strings.TrimSpace(key) == "" || len(key) > 100 {
		return apperr.Invalid("an Idempotency-Key of at most 100 bytes is required")
	}
	for _, p := range internalKeys {
		if strings.HasPrefix(key, p) {
			return apperr.Invalid("an Idempotency-Key may not start with " + p)
		}
	}
	return nil
}

// checkAmount rejects an amount that is not positive or has more decimals
// than its asset (never rounded, ADR-0008).
func checkAmount(amount decimal.Decimal, decimals int32) error {
	if !amount.IsPositive() {
		return apperr.Invalid("the amount must be positive")
	}
	if !amount.Equal(amount.Truncate(decimals)) {
		return apperr.New(apperr.KindInvalid, "MARGIN_AMOUNT_PRECISION", fmt.Sprintf("the amount has more than %d decimals", decimals)).
			WithDetail("decimals", decimals)
	}
	return nil
}

// Borrow lends the user an asset into a margin account (design §3.2,
// §4.3): within the account's room (MaxBorrow), the pool and the user's
// cap; the first hour's interest is charged with it. The same key with the
// same request returns the loan, with another COMMON_IDEMPOTENCY_CONFLICT.
// While spot trading is closed (product.spot) a borrow by hand is
// PRODUCT_CLOSED (review C61): a loan is new exposure on a line that takes
// none; an order's own borrow comes with an order spot-trading-service
// refuses then. Repaying and transfers out go on.
func (s *Service) Borrow(ctx context.Context, in BorrowInput) (ports.Loan, error) {
	if in.OrderID == "" {
		if err := checkKey(in.IdemKey); err != nil {
			return ports.Loan{}, err
		}
	} else if in.IdemKey != orderKey(in.OrderID) {
		return ports.Loan{}, fmt.Errorf("the borrow of order %s goes under %s", in.OrderID, orderKey(in.OrderID))
	}
	if prior, ok, err := s.Store.Read().Borrows().ByKey(ctx, in.UserID, in.IdemKey); err != nil {
		return ports.Loan{}, err
	} else if ok {
		return s.replayBorrow(ctx, prior, in)
	}
	if in.OrderID == "" && s.Features != nil && s.Features.Closed(flags.KeyProductSpot) {
		return ports.Loan{}, flags.ErrProductClosed(flags.KeyProductSpot)
	}
	if err := s.enabled(in.UserID); err != nil {
		return ports.Loan{}, err
	}
	if err := s.eligible(ctx, in.UserID, in.Account.Symbol); err != nil {
		return ports.Loan{}, err
	}
	var b ports.Borrow
	replay := false
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, in.UserID); err != nil {
			return err
		}
		prior, ok, err := r.Borrows().ByKey(ctx, in.UserID, in.IdemKey)
		if err != nil {
			return err
		}
		if b, replay = prior, ok; ok { // a concurrent retry won
			return nil
		}
		b, err = s.planBorrow(ctx, r, in)
		return err
	})
	if err != nil {
		return ports.Loan{}, err
	}
	if replay {
		return s.replayBorrow(ctx, b, in)
	}
	return s.postBorrow(ctx, b)
}

var domainIdempotencyConflict = apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict,
	"the idempotency key was used for a different request")

// planBorrow checks a borrow under the user's lock and the asset pool's,
// prices its first hour and stores it PENDING with the pool's amount
// reserved.
func (s *Service) planBorrow(ctx context.Context, r ports.Repos, in BorrowInput) (ports.Borrow, error) {
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return ports.Borrow{}, err
	}
	t, err := cat.Asset(in.Account, in.Asset)
	if err != nil {
		return ports.Borrow{}, err
	}
	if !t.Borrowable {
		return ports.Borrow{}, domain.ErrNotBorrowable.WithDetail("asset", in.Asset)
	}
	if err := checkAmount(in.Amount, t.Decimals); err != nil {
		return ports.Borrow{}, err
	}
	st, ok, err := r.Accounts().Get(ctx, in.UserID, in.Account)
	if err != nil {
		return ports.Borrow{}, err
	}
	if !ok {
		return ports.Borrow{}, domain.ErrLimit.WithDetail("max_borrowable", "0")
	}
	if !st.Status.Open() {
		return ports.Borrow{}, domain.ErrFrozen.WithDetail("status", string(st.Status))
	}
	lent, err := r.Pools().LockLent(ctx, in.Asset)
	if err != nil {
		return ports.Borrow{}, err
	}
	held, err := s.standingOf(ctx, r, in.UserID)
	if err != nil {
		return ports.Borrow{}, err
	}
	room, err := s.room(ctx, r, cat, held, in.Account, in.Asset, &lent)
	if err != nil {
		return ports.Borrow{}, err
	}
	if most, limit := domain.MaxBorrow(room); in.Amount.GreaterThan(most) {
		return ports.Borrow{}, borrowRefusal(room, in.Amount, most, limit)
	}
	now := s.Now()
	rate, err := s.hourRate(ctx, r, t, domain.Hour(now), lent)
	if err != nil {
		return ports.Borrow{}, err
	}
	b := ports.Borrow{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: in.UserID, Account: in.Account, Asset: in.Asset, Amount: in.Amount,
		Model: rate.Model, Rate: rate.Rate, FirstInterest: domain.Interest(in.Amount, rate.Rate, t.Decimals), OrderID: in.OrderID,
		IdemKey: in.IdemKey, RequestHash: in.hash(), Status: ports.OpPending, CreatedAt: now,
	}
	if err := r.Borrows().Insert(ctx, b); err != nil {
		return ports.Borrow{}, err
	}
	return b, r.Pools().AddLent(ctx, in.Asset, in.Amount)
}

// borrowRefusal names the bound a borrow broke: the pool, the user's cap,
// the warning level or the leverage.
func borrowRefusal(room domain.BorrowRoom, amount, most decimal.Decimal, limit domain.Limit) error {
	switch limit {
	case domain.LimitPool:
		return domain.ErrPoolEmpty.WithDetail("pool_available", decimal.Max(room.PoolLeft, decimal.Zero).String())
	case domain.LimitLevel:
		// The loan's value at its price, the asset counting at its haircut.
		value := amount.Mul(room.Price)
		haircut := decimal.Zero
		if room.Asset.Collateral {
			haircut = room.Asset.Haircut
		}
		after := domain.Valuation{
			TotalAsset: room.Valuation.TotalAsset.Add(value.Mul(haircut)), TotalLiability: room.Valuation.TotalLiability.Add(value),
		}
		level, _ := after.Level()
		return domain.ErrLevelTooLow.WithDetail("margin_level", level.String()).WithDetail("warn_level", room.Terms.WarnLevel.String()).
			WithDetail("max_borrowable", most.String())
	}
	return domain.ErrLimit.WithDetail("max_borrowable", most.String()).WithDetail("limited_by", string(limit))
}

// hourRate returns the asset's rate for an hour, setting it from the
// pool's use when it is not set yet (lent is the pool's amount now, the
// best value of the hour's while it lasts).
func (s *Service) hourRate(ctx context.Context, r ports.Repos, t domain.AssetTerms, hour time.Time, lent decimal.Decimal) (ports.Rate, error) {
	if got, ok, err := r.Rates().Get(ctx, t.Asset, hour); err != nil || ok {
		return got, err
	}
	return r.Rates().Set(ctx, ports.Rate{
		Asset: t.Asset, Hour: hour, Model: t.Model, Rate: t.HourlyRate(lent), Lent: lent, PoolCap: t.PoolCap,
	})
}

func (s *Service) replayBorrow(ctx context.Context, prior ports.Borrow, in BorrowInput) (ports.Loan, error) {
	if !bytes.Equal(prior.RequestHash, in.hash()) {
		return ports.Loan{}, domainIdempotencyConflict
	}
	if prior.Status == ports.OpPending {
		return s.postBorrow(ctx, prior)
	}
	if prior.Status == ports.OpFailed {
		return ports.Loan{}, failure(prior.Failure)
	}
	return s.Store.Read().Loans().Get(ctx, prior.UserID, prior.Account, prior.Asset)
}

// failureKinds names the kinds of the ledger's refusals (refused) as a
// write's failure records them.
var failureKinds = map[apperr.Kind]string{
	apperr.KindInvalid: "INVALID", apperr.KindNotFound: "NOT_FOUND", apperr.KindConflict: "CONFLICT",
	apperr.KindUnprocessable: "UNPROCESSABLE", apperr.KindForbidden: "FORBIDDEN",
}

// failureText records a refusal of the ledger: "[KIND] CODE: message".
func failureText(e *apperr.Error) string {
	name, ok := failureKinds[e.Kind]
	if !ok {
		name = failureKinds[apperr.KindUnprocessable]
	}
	return "[" + name + "] " + e.Code + ": " + e.Message
}

// failure rebuilds the refusal a write was recorded with, its kind
// included (review CK: a replay answers as the first time); a record
// without a kind is UNPROCESSABLE.
func failure(text string) error {
	kind := apperr.KindUnprocessable
	if rest, ok := strings.CutPrefix(text, "["); ok {
		if name, after, ok := strings.Cut(rest, "] "); ok {
			for k, n := range failureKinds {
				if n == name {
					kind = k
				}
			}
			text = after
		}
	}
	code, msg, _ := strings.Cut(text, ": ")
	return apperr.New(kind, code, msg)
}

// refused tells a business refusal of the ledger (the write fails for
// good) from a failure worth retrying.
func refused(err error) bool {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Kind {
	case apperr.KindInvalid, apperr.KindNotFound, apperr.KindConflict, apperr.KindUnprocessable, apperr.KindForbidden:
		return true
	}
	return false
}

// errInProgress answers a write whose ledger outcome is not known yet:
// recovery finishes it, and a retry with the same key reports it.
var errInProgress = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the request is being processed; retry with the same Idempotency-Key")

// postBorrow books a PENDING borrow in the ledger and records the outcome.
func (s *Service) postBorrow(ctx context.Context, b ports.Borrow) (ports.Loan, error) {
	moves := []ports.Move{{Type: domain.MoveBorrow, Asset: b.Asset, Amount: b.Amount}}
	if b.FirstInterest.IsPositive() {
		moves = append(moves, ports.Move{Type: domain.MoveInterest, Asset: b.Asset, Amount: b.FirstInterest})
	}
	journals, postErr := s.Ledger.Post(ctx, ports.Posting{
		IdemKey: "margin-borrow:" + b.ID, UserID: b.UserID, Account: b.Account, Reference: "borrow " + b.ID, Moves: moves,
	})
	if postErr != nil && !refused(postErr) {
		s.count("borrow", "pending")
		s.Log.WarnContext(ctx, "borrow not booked yet; recovery retries it", "borrow_id", b.ID, "error", postErr)
		return ports.Loan{}, errInProgress
	}
	var loan ports.Loan
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, b.UserID); err != nil {
			return err
		}
		cur, err := r.Borrows().GetForUpdate(ctx, b.ID)
		if err != nil {
			return err
		}
		if cur.Status != ports.OpPending { // recovery got here first
			loan, err = r.Loans().Get(ctx, b.UserID, b.Account, b.Asset)
			return err
		}
		cur.DoneAt = s.Now()
		if postErr != nil {
			cur.Status, cur.Failure = ports.OpFailed, failureText(apperr.From(postErr))
			if err := r.Borrows().Finish(ctx, cur); err != nil {
				return err
			}
			if _, err := r.Pools().LockLent(ctx, b.Asset); err != nil {
				return err
			}
			return r.Pools().AddLent(ctx, b.Asset, b.Amount.Neg())
		}
		cur.Status = ports.OpDone
		if err := r.Borrows().Finish(ctx, cur); err != nil {
			return err
		}
		if err := r.Loans().Add(ctx, b.UserID, b.Account, b.Asset, b.Amount, b.FirstInterest, cur.DoneAt); err != nil {
			return err
		}
		if loan, err = r.Loans().Get(ctx, b.UserID, b.Account, b.Asset); err != nil {
			return err
		}
		return s.recordBorrow(ctx, r, cur, loan, journals)
	})
	if err != nil {
		return ports.Loan{}, err
	}
	if postErr != nil {
		s.count("borrow", "failed")
		return ports.Loan{}, postErr
	}
	s.count("borrow", "done")
	s.touch(b.UserID)
	return loan, nil
}

// recordBorrow stores the first hour's charge and queues MarginBorrowed
// and MarginInterestAccrued.
func (s *Service) recordBorrow(ctx context.Context, r ports.Repos, b ports.Borrow, loan ports.Loan, journals []string) error {
	journal := func(i int) string {
		if i < len(journals) {
			return journals[i]
		}
		return ""
	}
	if err := r.Emit(ctx, event.TopicMargin, &marginv1.MarginBorrowed{
		BorrowId: b.ID, UserId: b.UserID, AccountType: string(b.Account.Type), Symbol: b.Account.Symbol, Asset: b.Asset,
		Amount: b.Amount.String(), InterestModel: string(b.Model), HourlyRate: b.Rate.String(), FirstInterest: b.FirstInterest.String(),
		Principal: loan.Principal.String(), Interest: loan.Interest.String(), OrderId: b.OrderID, JournalId: journal(0),
		BorrowedAt: timestamppb.New(b.CreatedAt),
	}, "user", b.UserID); err != nil {
		return err
	}
	if !b.FirstInterest.IsPositive() {
		return nil
	}
	c := ports.Charge{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: b.UserID, Account: b.Account, Asset: b.Asset, Hour: b.CreatedAt,
		Principal: b.Amount, Model: b.Model, Rate: b.Rate, Interest: b.FirstInterest, BorrowID: b.ID, Status: ports.OpDone,
		CreatedAt: b.CreatedAt, DoneAt: b.DoneAt,
	}
	if _, err := r.Interest().Insert(ctx, c); err != nil {
		return err
	}
	return r.Emit(ctx, event.TopicMargin, interestEvent(c, loan, journal(1)), "user", b.UserID)
}

func interestEvent(c ports.Charge, loan ports.Loan, journal string) *marginv1.MarginInterestAccrued {
	return &marginv1.MarginInterestAccrued{
		InterestId: c.ID, UserId: c.UserID, AccountType: string(c.Account.Type), Symbol: c.Account.Symbol, Asset: c.Asset,
		Principal: c.Principal.String(), InterestModel: string(c.Model), HourlyRate: c.Rate.String(), Interest: c.Interest.String(),
		InterestOwed: loan.Interest.String(), Hour: timestamppb.New(c.Hour), JournalId: journal,
	}
}
