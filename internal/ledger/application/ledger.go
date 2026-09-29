// Package application holds ledger-service's use cases: posting journals
// once per idempotency key, freezes, transfers and balances.
package application

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/lidp280504357/exchange/internal/ledger/domain"
	"github.com/lidp280504357/exchange/internal/ledger/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/tracing"
)

// Service is the ledger.
type Service struct {
	Store       ports.Store
	Assets      ports.Assets
	Eligibility ports.Eligibility
	Flags       ports.Flags
	// Futures tells the cross positions' unrealized result for transfers
	// out of FUTURES (derivatives-service); nil skips the check.
	Futures ports.Futures
	Log     *slog.Logger
	Now     func() time.Time
	// WelcomeCredits are the simulated funds of phase 1 (ledger.welcome_credit).
	WelcomeCredits []Credit
}

// Credit is an amount of an asset, as configured.
type Credit struct {
	Asset  string
	Amount decimal.Decimal
}

// Result is a posted (or replayed) journal.
type Result struct {
	JournalID string
	Seq       int64
	Replayed  bool
}

// Post writes p exactly once: the same key with the same content returns
// the first journal, with other content COMMON_IDEMPOTENCY_CONFLICT.
func (s *Service) Post(ctx context.Context, p domain.Posting) (Result, error) {
	if err := s.checkPrecision(ctx, p); err != nil {
		return Result{}, err
	}
	var res Result
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		res, err = s.post(ctx, r, p)
		return err
	})
	if c, ok := pg.UniqueViolation(err); ok && c == "journals_idem_key_key" {
		// A concurrent posting with the same key won: report it.
		return s.replay(ctx, s.Store.Read(), p)
	}
	return res, err
}

func (s *Service) replay(ctx context.Context, r ports.Repos, p domain.Posting) (Result, error) {
	j, err := r.Journals().ByIdemKey(ctx, p.IdemKey)
	if err != nil {
		return Result{}, err
	}
	if j == nil {
		return Result{}, errors.New("journal vanished after a key conflict")
	}
	if !bytes.Equal(j.RequestHash, p.Hash()) {
		return Result{}, domain.ErrIdempotencyConflict
	}
	return Result{JournalID: j.ID, Seq: j.Seq, Replayed: true}, nil
}

// checkPrecision rejects amounts with more decimals than their asset has.
func (s *Service) checkPrecision(ctx context.Context, p domain.Posting) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for _, asset := range p.Assets() {
		decimals, err := s.Assets.Decimals(ctx, asset)
		if err != nil {
			return err
		}
		for _, l := range p.Lines {
			if l.Account.Asset == asset && !l.Amount.Equal(l.Amount.Truncate(decimals)) {
				return apperr.New(apperr.KindInvalid, "LEDGER_AMOUNT_PRECISION", "the amount has too many decimals").
					WithDetail("asset", asset).WithDetail("decimals", decimals)
			}
		}
	}
	return nil
}

// post writes p inside r's transaction. Every balance is computed before
// anything is written, so a refusal (insufficient balance) leaves the
// transaction clean for the caller to continue.
func (s *Service) post(ctx context.Context, r ports.Repos, p domain.Posting) (Result, error) {
	if err := p.Validate(); err != nil {
		return Result{}, err
	}
	existing, err := r.Journals().ByIdemKey(ctx, p.IdemKey)
	if err != nil {
		return Result{}, err
	}
	if existing != nil {
		if !bytes.Equal(existing.RequestHash, p.Hash()) {
			return Result{}, domain.ErrIdempotencyConflict
		}
		return Result{JournalID: existing.ID, Seq: existing.Seq, Replayed: true}, nil
	}

	accounts, err := r.Accounts().Lock(ctx, p.Accounts())
	if err != nil {
		return Result{}, err
	}
	byKey := make(map[domain.AccountKey]*domain.Account, len(accounts))
	for i := range accounts {
		byKey[accounts[i].Key] = &accounts[i]
	}
	lines := make([]domain.PostedLine, 0, len(p.Lines))
	for _, l := range p.Lines {
		acc := byKey[l.Account]
		if err := acc.Apply(l); err != nil {
			return Result{}, err
		}
		lines = append(lines, domain.PostedLine{Account: *acc, Amount: l.Amount, Kind: l.Kind, AccountVersion: acc.Version})
	}

	j := domain.Journal{
		ID: uuid.Must(uuid.NewV7()).String(), IdemKey: p.IdemKey, RequestHash: p.Hash(), EntryType: p.EntryType,
		SourceEventID: p.SourceEventID, TraceID: tracing.TraceID(ctx), Memo: p.Memo, PostedAt: s.Now(),
	}
	if p.TraceID != "" {
		j.TraceID = p.TraceID
	}
	seq, err := r.Journals().Insert(ctx, j, lines)
	if err != nil {
		return Result{}, err
	}
	for _, a := range accounts {
		if err := r.Accounts().Save(ctx, a); err != nil {
			return Result{}, err
		}
	}
	if err := s.emit(ctx, r, j, seq, lines, accounts); err != nil {
		return Result{}, err
	}
	return Result{JournalID: j.ID, Seq: seq}, nil
}

func (s *Service) emit(ctx context.Context, r ports.Repos, j domain.Journal, seq int64, lines []domain.PostedLine, accounts []domain.Account) error {
	posted := &ledgerv1.EntryPosted{JournalId: j.ID, Seq: seq, EntryType: j.EntryType, IdempotencyKey: j.IdemKey, Memo: j.Memo}
	for _, l := range lines {
		posted.Lines = append(posted.Lines, &ledgerv1.EntryLine{
			AccountId: l.Account.ID, OwnerType: l.Account.Key.OwnerType, OwnerId: l.Account.Key.OwnerID,
			AccountType: l.Account.Key.Type, Asset: l.Account.Key.Asset, Amount: l.Amount.String(), BalanceKind: l.Kind,
			AvailableAfter: l.Account.Available.String(), FrozenAfter: l.Account.Frozen.String(),
		})
	}
	if err := r.Emit(ctx, event.TopicLedger, posted, "journal", j.ID); err != nil {
		return err
	}
	for _, a := range accounts {
		if a.Key.OwnerType != domain.OwnerUser {
			continue
		}
		if err := r.Emit(ctx, event.TopicLedger, &ledgerv1.BalanceChanged{
			AccountId: a.ID, UserId: a.Key.OwnerID, AccountType: a.Key.Type, Asset: a.Key.Asset,
			Available: a.Available.String(), Frozen: a.Frozen.String(), JournalId: j.ID, EntryType: j.EntryType,
		}, "account", a.ID); err != nil {
			return err
		}
	}
	return nil
}

// Freeze moves amount from available to frozen.
func (s *Service) Freeze(ctx context.Context, idemKey, entryType, userID, accountType, asset string, amount decimal.Decimal, reference string) (Result, error) {
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return Result{}, err
	}
	p, err := domain.FreezePosting(scoped(userID, idemKey), entryType, userID, accountType, asset, amount, decimals, reference)
	if err != nil {
		return Result{}, err
	}
	return s.Post(ctx, p)
}

// Unfreeze moves amount from frozen back to available.
func (s *Service) Unfreeze(ctx context.Context, idemKey, entryType, userID, accountType, asset string, amount decimal.Decimal, reference string) (Result, error) {
	decimals, err := s.Assets.Decimals(ctx, asset)
	if err != nil {
		return Result{}, err
	}
	p, err := domain.UnfreezePosting(scoped(userID, idemKey), entryType, userID, accountType, asset, amount, decimals, reference)
	if err != nil {
		return Result{}, err
	}
	return s.Post(ctx, p)
}

// scoped keeps callers' keys apart per user.
func scoped(userID, key string) string { return "u:" + userID + ":" + key }

// Balances lists a user's accounts, optionally of one type.
func (s *Service) Balances(ctx context.Context, userID, accountType string) ([]domain.Account, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, apperr.Invalid("user_id must be a UUID")
	}
	if accountType != "" && accountType != domain.AccountSpot && accountType != domain.AccountFutures {
		return nil, apperr.Invalid("account_type must be SPOT or FUTURES")
	}
	return s.Store.Read().Accounts().ByOwner(ctx, userID, accountType)
}

// Entries returns a page of a user's fund flow and the next cursor.
func (s *Service) Entries(ctx context.Context, userID, asset, entryType string, before int64, limit int) ([]domain.Entry, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	entries, err := s.Store.Read().Journals().Entries(ctx, userID, asset, entryType, before, limit+1)
	if err != nil {
		return nil, 0, err
	}
	var next int64
	if len(entries) > limit {
		entries = entries[:limit]
		next = entries[limit-1].ID
	}
	return entries, next, nil
}
