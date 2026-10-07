package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountv1 "github.com/skill/exchange/api/gen/go/exchange/account/v1"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/ledger/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
)

// TransferInput is a transfer between a user's SPOT and FUTURES accounts.
type TransferInput struct {
	UserID  string
	IdemKey string
	Asset   string
	Amount  decimal.Decimal
	From    string
	To      string
}

func (in TransferInput) hash() []byte {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%s", in.Asset, in.Amount.String(), in.From, in.To))
	return sum[:]
}

// Transfer settles a transfer in one transaction (§13 acceptance 11): it
// completes, or fails for lack of funds, and either outcome is kept, so a
// retry with the same key gets the same answer.
func (s *Service) Transfer(ctx context.Context, in TransferInput) (domain.Transfer, error) {
	if in.IdemKey == "" || len(in.IdemKey) > 100 {
		return domain.Transfer{}, apperr.Invalid("an Idempotency-Key of at most 100 bytes is required")
	}
	decimals, err := s.Assets.Decimals(ctx, in.Asset)
	if err != nil {
		return domain.Transfer{}, err
	}
	p, err := domain.TransferPosting("", in.UserID, in.Asset, in.Amount, decimals, in.From, in.To)
	if err != nil {
		return domain.Transfer{}, err
	}
	if prior, err := s.Store.Read().Transfers().ByKey(ctx, in.UserID, in.IdemKey); err != nil {
		return domain.Transfer{}, err
	} else if prior != nil {
		return replayTransfer(*prior, in)
	}
	if line := futuresLine(in.Asset); in.To == domain.AccountFutures && s.Flags.Closed(line) {
		return domain.Transfer{}, flags.ErrProductClosed(line)
	}
	allowed, reason, err := s.Eligibility.Check(ctx, in.UserID, "TRANSFER")
	if err != nil {
		return domain.Transfer{}, err
	}
	if !allowed {
		return domain.Transfer{}, apperr.New(apperr.KindForbidden, reason, "transfers are not available to this account now")
	}

	// What leaves FUTURES must not leave a cross position's unrealized loss
	// uncovered: at most min(available, available + that result).
	unrealized := decimal.Zero
	guarded := in.From == domain.AccountFutures && s.Futures != nil
	if guarded {
		if unrealized, err = s.Futures.CrossUnrealizedPnL(ctx, in.UserID, in.Asset); err != nil {
			return domain.Transfer{}, err
		}
	}

	t := domain.Transfer{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: in.UserID, IdemKey: in.IdemKey, RequestHash: in.hash(),
		Asset: in.Asset, Amount: in.Amount, From: in.From, To: in.To, CreatedAt: s.Now(),
	}
	p.IdemKey = "transfer:" + t.ID
	var outcome error
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		var res Result
		var err error
		if guarded {
			err = s.checkTransferable(ctx, r, p, in, unrealized)
		}
		if err == nil {
			res, err = s.post(ctx, r, p)
		}
		switch {
		case apperr.Is(err, "LEDGER_INSUFFICIENT_BALANCE"):
			t.Status, t.FailureReason, outcome = domain.TransferFailed, "LEDGER_INSUFFICIENT_BALANCE", err
		case err != nil:
			return err
		default:
			t.Status, t.JournalID = domain.TransferCompleted, res.JournalID
		}
		if err := r.Transfers().Insert(ctx, t); err != nil {
			return err
		}
		return s.emitTransfer(ctx, r, t)
	})
	if c, ok := pgUnique(err); ok && c == "transfers_user_id_idem_key_key" {
		// A concurrent request with the same key won.
		prior, err := s.Store.Read().Transfers().ByKey(ctx, in.UserID, in.IdemKey)
		if err != nil || prior == nil {
			return domain.Transfer{}, fmt.Errorf("transfer vanished after a key conflict: %w", err)
		}
		return replayTransfer(*prior, in)
	}
	if err != nil {
		return domain.Transfer{}, err
	}
	if outcome != nil {
		return t, withTransfer(outcome, t)
	}
	return t, nil
}

// futuresLine is the product line a FUTURES balance of asset serves
// (design 2026-10-07, product switches): USDT's the USDT-margined
// contracts', any other asset's (BTC, ETH, ASTRA) the coin-margined ones'.
// While it is closed nothing moves in; what is there may move out.
func futuresLine(asset string) string {
	if asset == "USDT" {
		return flags.KeyProductUSDTM
	}
	return flags.KeyProductCoinM
}

func replayTransfer(prior domain.Transfer, in TransferInput) (domain.Transfer, error) {
	if !bytes.Equal(prior.RequestHash, in.hash()) {
		return domain.Transfer{}, domain.ErrIdempotencyConflict
	}
	if prior.Status == domain.TransferFailed {
		return prior, withTransfer(domain.ErrInsufficientBalance, prior)
	}
	return prior, nil
}

func withTransfer(err error, t domain.Transfer) error {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return err
	}
	return ae.WithDetail("transfer_id", t.ID).WithDetail("status", t.Status)
}

func (s *Service) emitTransfer(ctx context.Context, r ports.Repos, t domain.Transfer) error {
	if t.Status == domain.TransferCompleted {
		return r.Emit(ctx, event.TopicAccount, &accountv1.AccountTransferCompleted{
			TransferId: t.ID, UserId: t.UserID, Asset: t.Asset, Amount: t.Amount.String(),
			FromAccountType: t.From, ToAccountType: t.To, JournalId: t.JournalID,
		}, "user", t.UserID)
	}
	return r.Emit(ctx, event.TopicAccount, &accountv1.AccountTransferFailed{
		TransferId: t.ID, UserId: t.UserID, Asset: t.Asset, Amount: t.Amount.String(),
		FromAccountType: t.From, ToAccountType: t.To, Reason: t.FailureReason,
	}, "user", t.UserID)
}

// Transfers returns a page of a user's transfers and the next cursor.
func (s *Service) Transfers(ctx context.Context, userID, before string, limit int) ([]domain.Transfer, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if before != "" {
		if _, err := uuid.Parse(before); err != nil {
			return nil, "", apperr.Invalid("invalid cursor")
		}
	}
	list, err := s.Store.Read().Transfers().List(ctx, userID, before, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].ID
	}
	return list, next, nil
}

// checkTransferable refuses a transfer out of FUTURES beyond
// min(available, available + the cross positions' unrealized result),
// under the lock of the account.
func (s *Service) checkTransferable(ctx context.Context, r ports.Repos, p domain.Posting, in TransferInput, unrealized decimal.Decimal) error {
	accounts, err := r.Accounts().Lock(ctx, p.Accounts())
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if a.Key != domain.UserAccount(in.UserID, domain.AccountFutures, in.Asset) {
			continue
		}
		limit := decimal.Max(decimal.Min(a.Available, a.Available.Add(unrealized)), decimal.Zero)
		if in.Amount.GreaterThan(limit) {
			return domain.ErrInsufficientBalance.WithDetail("asset", in.Asset).WithDetail("account_type", domain.AccountFutures).
				WithDetail("transferable", limit.String())
		}
	}
	return nil
}
