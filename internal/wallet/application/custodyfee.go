package application

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// feeBound is how many times its network's withdrawal fee (what users pay)
// a custodian's fee may be before a person looks at it (review ④): a fee
// counted in another unit is off by far more (gas in the chain's smallest
// unit; TRX taken for USDT, 13.6 against TRC20's 1).
const feeBound = 5

// custodyFee works out what becomes of the fee a custodian reports for a
// withdrawal it sent (review ④): booked from GAS_SUPPLY (FeeBookable), held
// for a person (FeeHeld: above feeBound times the network's withdrawal fee
// or the amount sent, without a bound to compare with, or a token whose
// fee unit nobody confirmed), or nothing (charged outside the coin
// balances, FeeUnitOutside). The note says what happened, for the
// callback's log; a held fee is counted and logged by after, once the
// callback's transaction committed.
func (s *Service) custodyFee(ctx context.Context, r ports.Repos, provider string, w domain.Withdrawal, t ports.CustodyTrade, after *[]func(),
) (*domain.ChainFee, string, error) {
	if !t.Fee.IsPositive() {
		return nil, "", nil
	}
	f := domain.ChainFee{
		TxHash: provider + ":" + t.TradeID, Network: w.Network, Asset: w.Asset, Amount: t.Fee, Purpose: domain.FeeWithdrawal, Reference: w.ID,
		Status: domain.FeeBookable,
	}
	reported := fmt.Sprintf("%s %s (%s at %d decimals)", t.Fee, w.Asset, t.Fee.Shift(t.Decimals), t.Decimals)
	hold := func(why string) (*domain.ChainFee, string, error) {
		f.Status, f.HoldReason = domain.FeeHeld, reported+": "+why
		*after = append(*after, func() {
			if s.FeesHeld != nil {
				s.FeesHeld.Inc()
			}
			s.Log.ErrorContext(ctx, "the custodian's fee is held for a person: book it or write it off (exchangectl wallet custody-fee)",
				"withdrawal_id", w.ID, "fee", reported, "why", why)
		})
		return &f, "; fee " + reported + " held for a person: " + why, nil
	}
	c := s.Custodians[provider]
	net, err := s.Networks.Network(ctx, w.Asset, w.Network)
	if err != nil {
		return nil, "", err
	}
	u, err := r.ChainFees().Unit(ctx, provider, w.Asset, w.Network)
	if err != nil {
		return nil, "", err
	}
	unit := domain.FeeUnitSelf
	switch {
	case u != nil:
		unit = u.Unit
	case s.token(ctx, c, t.Coin):
		return hold(fmt.Sprintf("nobody has confirmed how %s counts its fee on %s %s, a token (exchangectl wallet custody-fee-unit)",
			provider, w.Asset, w.Network))
	}
	switch unit {
	case domain.FeeUnitOutside:
		return nil, "; fee " + reported + " not booked: charged outside the coin balances (" + domain.FeeUnitOutside + ")", nil
	case domain.FeeUnitMain:
		chain, decimals, ok, err := s.chainCoin(ctx, c, provider, t.Coin)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			// Kept in that coin, as the custodian's list names it: TRX taken
			// for a TRC20 transfer is no USDT missing, so the custody check
			// must not count it against USDT (review AF).
			f.Asset, f.Amount = s.mainCoin(ctx, c, t)
			return hold("its unit is the chain's own coin, which the platform holds with " + provider + " on no network")
		}
		f.Asset, f.Amount, net = chain.Asset, t.Fee.Shift(t.Decimals).Shift(-decimals), chain
	}
	// Up to the asset's decimals the ledger books in: the custodian took
	// the whole fee, a fraction more booked leaves no shortfall behind.
	f.Amount = f.Amount.RoundCeil(net.Decimals)
	if !net.WithdrawFee.IsPositive() {
		// A network that charges users nothing: nothing tells a plausible
		// fee from one in another unit (the amount sent alone allows one
		// as large as itself).
		return hold("no bound to compare it with: the network of " + f.Asset + " charges no withdrawal fee")
	}
	limit := net.WithdrawFee.Mul(decimal.NewFromInt(feeBound))
	if unit != domain.FeeUnitMain && w.Amount.LessThan(limit) {
		limit = w.Amount
	}
	if f.Amount.GreaterThan(limit) {
		return hold(fmt.Sprintf("above %s %s, the lesser of the amount sent and %d times the network's withdrawal fee", limit, f.Asset,
			feeBound))
	}
	return &f, "", nil
}

// token reports whether the custodian lists coin as a token
// (support-coins tokenStatus 1); while its list cannot be read, a coin
// code whose coinType is not its mainCoinType is one.
func (s *Service) token(ctx context.Context, c ports.Custody, coin string) bool {
	if c != nil {
		if k, ok := s.coin(ctx, c, coin); ok {
			return k.Token
		}
	}
	main, sub, _ := strings.Cut(coin, ":")
	return main != sub
}

// chainCoin is the custodian's network of the chain's own coin of coin
// ("60:0xdac1..." → "60:60") and the decimals the custodian counts it in;
// false when the platform holds that coin with it on no network.
func (s *Service) chainCoin(ctx context.Context, c ports.Custody, provider, coin string) (domain.Network, int32, bool, error) {
	main, _, _ := strings.Cut(coin, ":")
	code := main + ":" + main
	net, ok, err := s.networkOfCoin(ctx, provider, code)
	if err != nil || !ok {
		return domain.Network{}, 0, false, err
	}
	decimals := net.Decimals
	if c != nil {
		if k, ok := s.coin(ctx, c, code); ok {
			decimals = k.Decimals
		}
	}
	return net, decimals, true, nil
}

// mainCoin is the chain's own coin of a trade's coin as the custodian lists
// it, and the trade's fee in it: its symbol and decimals when the list has
// it, else its code and the fee as reported.
func (s *Service) mainCoin(ctx context.Context, c ports.Custody, t ports.CustodyTrade) (string, decimal.Decimal) {
	main, _, _ := strings.Cut(t.Coin, ":")
	code := main + ":" + main
	if c != nil {
		if k, ok := s.coin(ctx, c, code); ok && k.Symbol != "" {
			return k.Symbol, t.Fee.Shift(t.Decimals).Shift(-k.Decimals)
		}
	}
	return code, t.Fee
}

// Custodied returns the decimals the ledger books an asset in when the
// custodian holds it for the platform on network (the fee's: a
// withdrawal's asset, or the chain's own coin of a token's network),
// false otherwise: a custodian's fee is booked only in such an asset.
type Custodied func(ctx context.Context, provider, asset, network string) (int32, bool, error)

// FeeResolution is a person's decision on a custodian's fee held for them
// (review ④): Book it from GAS_SUPPLY, in the Asset and Amount they found
// the custodian charged (as reported when empty), or write it off (not
// taken from the coin balances, or reported in another unit). A fee that
// waits for GAS_SUPPLY may be written off too (nothing to fund it from);
// should the ledger book it meanwhile, the booking stands.
type FeeResolution struct {
	WithdrawalID string
	Book         bool
	Asset        string
	Amount       decimal.Decimal
	Actor        string
	Reason       string
}

// ResolveCustodyFee records a person's decision on the fee held for a
// withdrawal, with an audit event; the processor books a booked one
// within a round.
func ResolveCustodyFee(ctx context.Context, store ports.Store, custodied Custodied, d FeeResolution, now time.Time) (domain.ChainFee, error) {
	if strings.TrimSpace(d.Actor) == "" || len(strings.TrimSpace(d.Reason)) < 3 {
		return domain.ChainFee{}, apperr.Invalid("an actor and a reason are required")
	}
	if d.Amount.IsNegative() || (!d.Book && (d.Asset != "" || !d.Amount.IsZero())) {
		return domain.ChainFee{}, apperr.Invalid("a positive amount, and an asset or amount only for a fee booked")
	}
	var out domain.ChainFee
	err := store.Tx(ctx, func(r ports.Repos) error {
		w, err := r.Withdrawals().Get(ctx, d.WithdrawalID)
		if err != nil {
			return err
		}
		if w == nil || w.Provider == "" {
			return apperr.NotFound("no such withdrawal with a custodian")
		}
		fees, err := r.ChainFees().OfReference(ctx, w.ID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(fees, func(f domain.ChainFee) bool {
			return f.Status == domain.FeeHeld || f.Status == domain.FeeBookable && f.JournalID == "" && !d.Book
		})
		if i < 0 {
			return apperr.NotFound("no fee of withdrawal " + w.ID + " waits for a person")
		}
		f := fees[i]
		before := f
		f.Status, f.ResolvedBy, f.Resolution, f.ResolvedAt = domain.FeeWrittenOff, d.Actor, d.Reason, now
		if f.HoldReason == "" {
			f.HoldReason = "waited to be booked (GAS_SUPPLY short)"
		}
		action := "wallet.custody.fee.write_off"
		if d.Book {
			f.Status, action = domain.FeeBookable, "wallet.custody.fee.book"
			if d.Asset != "" {
				f.Asset = strings.ToUpper(d.Asset)
			}
			if d.Amount.IsPositive() {
				f.Amount = d.Amount
			}
			decimals, ok, err := custodied(ctx, w.Provider, f.Asset, f.Network)
			if err != nil {
				return err
			}
			if !ok {
				return apperr.Invalid(fmt.Sprintf("the platform holds no %s with %s on %s: write the fee off instead", f.Asset, w.Provider,
					f.Network))
			}
			if !f.Amount.Equal(f.Amount.Truncate(decimals)) {
				return apperr.Invalid(fmt.Sprintf("%s is booked in at most %d decimals", f.Asset, decimals))
			}
		}
		if done, err := r.ChainFees().Resolve(ctx, f); err != nil || !done {
			if err == nil {
				err = apperr.New(apperr.KindConflict, apperr.CodeConflict, "the fee is no longer held")
			}
			return err
		}
		out = f
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "withdrawal:" + w.ID, Action: action, Actor: d.Actor, Reason: d.Reason,
			Details: fmt.Sprintf(`{"provider":%q,"fee":%q,"reported":%q,"booked":%q,"held_for":%q}`, w.Provider, f.TxHash,
				before.Amount.String()+" "+before.Asset, bookedAs(f), before.HoldReason),
		}, d.Actor)
	})
	return out, err
}

func bookedAs(f domain.ChainFee) string {
	if f.Status != domain.FeeBookable {
		return ""
	}
	return f.Amount.String() + " " + f.Asset
}

// SetCustodyFeeUnit records how a custodian counts its fee on one of its
// networks, as a person confirmed it (review ④), with an audit event.
// Fees held before stay held: each is booked or written off on its own.
func SetCustodyFeeUnit(ctx context.Context, store ports.Store, u domain.FeeUnit) error {
	u.Unit = strings.ToUpper(u.Unit)
	switch {
	case !slices.Contains(domain.FeeUnits, u.Unit):
		return apperr.Invalid(fmt.Sprintf("the unit is one of %v", domain.FeeUnits))
	case u.Provider == "" || u.Asset == "" || u.Network == "":
		return apperr.Invalid("a custodian, an asset and a network are required")
	case strings.TrimSpace(u.ConfirmedBy) == "" || len(strings.TrimSpace(u.Reason)) < 3:
		return apperr.Invalid("an actor and a reason (how it was confirmed) are required")
	}
	return store.Tx(ctx, func(r ports.Repos) error {
		before, err := r.ChainFees().Unit(ctx, u.Provider, u.Asset, u.Network)
		if err != nil {
			return err
		}
		if err := r.ChainFees().PutUnit(ctx, u); err != nil {
			return err
		}
		from := ""
		if before != nil {
			from = before.Unit
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "network:" + u.Asset + "/" + u.Network, Action: "wallet.custody.fee_unit", Actor: u.ConfirmedBy, Reason: u.Reason,
			Details: fmt.Sprintf(`{"provider":%q,"from":%q,"to":%q}`, u.Provider, from, u.Unit),
		}, u.ConfirmedBy)
	})
}
