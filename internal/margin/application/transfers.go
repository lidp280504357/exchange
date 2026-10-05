package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// TransferInput moves an asset between SPOT and a margin account.
type TransferInput struct {
	UserID    string
	IdemKey   string
	Direction domain.Direction
	Account   domain.Account
	Asset     string
	Amount    decimal.Decimal
}

func (in TransferInput) hash() []byte {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%s", in.Direction, in.Account.Key(), in.Asset, in.Amount.String()))
	return sum[:]
}

// Transfer moves an asset between SPOT and a margin account (design
// §3.2). IN opens the account on its first transfer and takes only the
// assets the account may hold; it needs margin.enabled, the MARGIN_TRADE
// eligibility and an open account. OUT takes back what MaxTransferOut
// allows — what is free of orders and of the asset's own debt, keeping
// the margin level at or above the warning level — and, lowering no one's
// risk but the user's, asks neither; a frozen or liquidating account
// moves nothing.
func (s *Service) Transfer(ctx context.Context, in TransferInput) (ports.Transfer, error) {
	if err := checkKey(in.IdemKey); err != nil {
		return ports.Transfer{}, err
	}
	if prior, ok, err := s.Store.Read().Transfers().ByKey(ctx, in.UserID, in.IdemKey); err != nil {
		return ports.Transfer{}, err
	} else if ok {
		return s.replayTransfer(ctx, prior, in)
	}
	if in.Direction == domain.DirectionIn {
		if err := s.enabled(in.UserID); err != nil {
			return ports.Transfer{}, err
		}
		if err := s.eligible(ctx, in.UserID, in.Account.Symbol); err != nil {
			return ports.Transfer{}, err
		}
	}
	var t ports.Transfer
	replay := false
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, in.UserID); err != nil {
			return err
		}
		prior, ok, err := r.Transfers().ByKey(ctx, in.UserID, in.IdemKey)
		if err != nil {
			return err
		}
		if t, replay = prior, ok; ok {
			return nil
		}
		t, err = s.planTransfer(ctx, r, in)
		return err
	})
	if err != nil {
		return ports.Transfer{}, err
	}
	if replay {
		return s.replayTransfer(ctx, t, in)
	}
	return s.postTransfer(ctx, t)
}

func (s *Service) planTransfer(ctx context.Context, r ports.Repos, in TransferInput) (ports.Transfer, error) {
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return ports.Transfer{}, err
	}
	terms, err := cat.Terms(in.Account)
	if err != nil {
		if in.Direction == domain.DirectionIn {
			return ports.Transfer{}, err
		}
		terms = domain.DefaultTerms(in.Account.Type, 3) // a pair dropped from the list still lets its funds out
	}
	at, err := cat.Asset(in.Account, in.Asset)
	if err != nil && in.Direction == domain.DirectionIn {
		return ports.Transfer{}, err
	}
	decimals := at.Decimals
	if err != nil { // an asset dropped from the list still leaves
		if decimals, err = s.Instruments.Decimals(ctx, in.Asset); err != nil {
			return ports.Transfer{}, err
		}
	}
	if err := checkAmount(in.Amount, decimals); err != nil {
		return ports.Transfer{}, err
	}
	now := s.Now()
	if in.Direction == domain.DirectionIn {
		st, err := r.Accounts().Ensure(ctx, in.UserID, in.Account, now)
		if err != nil {
			return ports.Transfer{}, err
		}
		if !st.Status.Open() {
			return ports.Transfer{}, domain.ErrFrozen.WithDetail("status", string(st.Status))
		}
	} else {
		st, ok, err := r.Accounts().Get(ctx, in.UserID, in.Account)
		if err != nil {
			return ports.Transfer{}, err
		}
		if !ok {
			return ports.Transfer{}, ErrInsufficient.WithDetail("asset", in.Asset)
		}
		if st.Status == domain.StatusLiquidating || st.Status == domain.StatusFrozen {
			return ports.Transfer{}, domain.ErrFrozen.WithDetail("status", string(st.Status))
		}
		if err := s.checkOut(ctx, r, cat, terms, in, decimals); err != nil {
			return ports.Transfer{}, err
		}
	}
	t := ports.Transfer{
		ID: uuid.Must(uuid.NewV7()).String(), UserID: in.UserID, Direction: in.Direction, Account: in.Account, Asset: in.Asset,
		Amount: in.Amount, IdemKey: in.IdemKey, RequestHash: in.hash(), Status: ports.OpPending, CreatedAt: now,
	}
	return t, r.Transfers().Insert(ctx, t)
}

// checkOut refuses a transfer out beyond MaxTransferOut, the account
// valued as it stands (the borrows and transfers out on their way
// counted): with debts it needs every asset of it priced.
func (s *Service) checkOut(ctx context.Context, r ports.Repos, cat Catalog, terms domain.Terms, in TransferInput, decimals int32) error {
	st, err := s.standingOf(ctx, r, in.UserID)
	if err != nil {
		return err
	}
	holdings := st.of(in.Account)
	h := holding(holdings, in.Asset)
	prices := s.Prices.Prices()
	v := domain.Value(holdings, cat.Assets, prices)
	price, haircut := decimal.Zero, decimal.Zero
	if v.HasDebt() {
		if !v.Complete() {
			return domain.ErrPriceUnavailable.WithDetail("asset", v.Unpriced[0])
		}
		if t, ok := cat.Assets[in.Asset]; ok && t.Collateral {
			p, ok := prices.Of(in.Asset)
			if !ok {
				return domain.ErrPriceUnavailable.WithDetail("asset", in.Asset)
			}
			price, haircut = p.Value, t.Haircut
		}
	}
	most := domain.MaxTransferOut(h, v, terms, price, haircut, decimals)
	if !in.Amount.GreaterThan(most) {
		return nil
	}
	if in.Amount.LessThanOrEqual(h.Free) && in.Amount.LessThanOrEqual(h.Total().Sub(h.Debt())) {
		level, _ := v.Level()
		return domain.ErrLevelTooLow.WithDetail("margin_level", level.String()).WithDetail("warn_level", terms.WarnLevel.String()).
			WithDetail("max_transferable", most.String())
	}
	return ErrInsufficient.WithDetail("asset", in.Asset).WithDetail("max_transferable", most.String())
}

func (s *Service) replayTransfer(ctx context.Context, prior ports.Transfer, in TransferInput) (ports.Transfer, error) {
	if !bytes.Equal(prior.RequestHash, in.hash()) {
		return ports.Transfer{}, domainIdempotencyConflict
	}
	switch prior.Status {
	case ports.OpPending:
		return s.postTransfer(ctx, prior)
	case ports.OpFailed:
		return ports.Transfer{}, failure(prior.Failure)
	}
	return prior, nil
}

// postTransfer books a PENDING transfer and records the outcome.
func (s *Service) postTransfer(ctx context.Context, t ports.Transfer) (ports.Transfer, error) {
	move := domain.MoveTransferIn
	if t.Direction == domain.DirectionOut {
		move = domain.MoveTransferOut
	}
	_, postErr := s.Ledger.Post(ctx, ports.Posting{
		IdemKey: "margin-transfer:" + t.ID, UserID: t.UserID, Account: t.Account, Reference: "transfer " + t.ID,
		Moves: []ports.Move{{Type: move, Asset: t.Asset, Amount: t.Amount}},
	})
	if postErr != nil && !refused(postErr) {
		s.count("transfer", "pending")
		s.Log.WarnContext(ctx, "transfer not booked yet; recovery retries it", "transfer_id", t.ID, "error", postErr)
		return ports.Transfer{}, errInProgress
	}
	var out ports.Transfer
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		cur, err := r.Transfers().GetForUpdate(ctx, t.ID)
		if err != nil {
			return err
		}
		out = cur
		if cur.Status != ports.OpPending {
			return nil
		}
		cur.DoneAt, cur.Status = s.Now(), ports.OpDone
		if postErr != nil {
			cur.Status, cur.Failure = ports.OpFailed, failureText(apperr.From(postErr))
		}
		out = cur
		return r.Transfers().Finish(ctx, cur)
	})
	if err != nil {
		return ports.Transfer{}, err
	}
	if postErr != nil {
		s.count("transfer", "failed")
		return ports.Transfer{}, postErr
	}
	s.count("transfer", "done")
	return out, nil
}
