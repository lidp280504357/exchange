package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The simulated market's coin from the console (ASTRA design §5.2, §6.1):
// who holds it, and more of it (or USDT) for the bots. User decision
// 2026-10-03: no transfers between bots; their pool grows by manual
// adjustments, here one fund operation that books one per bot.

// simTopHolders is how many of the largest holders the coin's page lists.
const simTopHolders = 20

// SimToken is how the simulated market's coin is held.
type SimToken struct {
	Asset string
	// Price is the pair's last trade as market-sim reads it; nil before
	// the first.
	Price *decimal.Decimal
	ports.Holdings
	At time.Time
}

// simBot is a bot as market-sim lists it.
type simBot struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	Label  string `json:"label"`
}

// simState is what the console reads of market-sim's state for the coin
// and its bots.
type simState struct {
	Symbol    string   `json:"symbol"`
	LastPrice *string  `json:"last_price"`
	Bots      []simBot `json:"bots"`
}

// coin is the simulated market's coin: its pair's base.
func (st simState) coin() string {
	base, _, _ := strings.Cut(st.Symbol, "-")
	return base
}

func (s *Service) simState(ctx context.Context) (simState, error) {
	raw, err := s.Sim.Status(ctx)
	if err != nil {
		return simState{}, err
	}
	var st simState
	if err := json.Unmarshal(raw, &st); err != nil || st.Symbol == "" {
		return simState{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-sim answered without its market")
	}
	return st, nil
}

// SimTokenHoldings returns who holds the simulated market's coin: the
// bots, the users, the system accounts and the largest holders, as the
// ledger holds it now (A123: the read model's lines it once summed expire
// after 15 days).
func (s *Service) SimTokenHoldings(ctx context.Context, p Principal) (SimToken, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return SimToken{}, err
	}
	st, err := s.simState(ctx)
	if err != nil {
		return SimToken{}, err
	}
	bots := make(map[string]bool, len(st.Bots))
	for _, b := range st.Bots {
		bots[b.UserID] = true
	}
	// Up to seven reads of ledger-service, bounded together (A125).
	read, cancel := context.WithTimeout(ctx, coinReadTimeout)
	defer cancel()
	holders, system, err := s.coinBalances(read, st.coin())
	if err != nil {
		return SimToken{}, err
	}
	h, err := holdingsOf(holders, bots, system, simTopHolders)
	if err != nil {
		return SimToken{}, err
	}
	out := SimToken{Asset: st.coin(), Holdings: h, At: s.Now()}
	if st.LastPrice != nil {
		if px, err := decimal.NewFromString(*st.LastPrice); err == nil {
			out.Price = &px
		}
	}
	return out, nil
}

// coinReadTimeout bounds the reads of who holds the coin.
const coinReadTimeout = 10 * time.Second

// coinBalances reads an asset's holders and system accounts as one moment:
// the holders again after the system accounts, until their total held
// still (a trade's fee moves the coin from a holder to FEE_REVENUE
// between two reads); three tries, then the last read stands.
func (s *Service) coinBalances(ctx context.Context, asset string) ([]ports.Holder, []ports.Balance, error) {
	holders, err := s.Ledger.Holders(ctx, asset)
	if err != nil {
		return nil, nil, err
	}
	var system []ports.Balance
	for range 3 {
		if system, err = s.Ledger.SystemBalances(ctx, asset); err != nil {
			return nil, nil, err
		}
		again, err := s.Ledger.Holders(ctx, asset)
		if err != nil {
			return nil, nil, err
		}
		before, after := heldBy(holders), heldBy(again)
		holders = again
		if before.Equal(after) {
			break
		}
	}
	return holders, system, nil
}

// heldBy is what the holders hold together.
func heldBy(holders []ports.Holder) decimal.Decimal {
	sum := decimal.Zero
	for _, h := range holders {
		sum = sum.Add(h.Amount)
	}
	return sum
}

// holdingsOf sums the holders of an asset: the bots apart from the other
// users (what each kind holds, owing included, and how many hold some),
// the system accounts by type (those not at zero) and the top largest
// holders.
func holdingsOf(holders []ports.Holder, bots map[string]bool, system []ports.Balance, top int) (ports.Holdings, error) {
	out := ports.Holdings{System: map[string]decimal.Decimal{}, Top: []ports.Holder{}}
	for _, h := range holders {
		some := h.Amount.IsPositive()
		if bots[h.UserID] {
			out.Bots = out.Bots.Add(h.Amount)
			if some {
				out.BotHolders++
			}
		} else {
			out.Users = out.Users.Add(h.Amount)
			if some {
				out.UserHolders++
			}
		}
		if some {
			out.Top = append(out.Top, h)
		}
	}
	slices.SortStableFunc(out.Top, func(a, b ports.Holder) int {
		if c := b.Amount.Cmp(a.Amount); c != 0 {
			return c
		}
		return strings.Compare(a.UserID, b.UserID)
	})
	out.Top = out.Top[:min(len(out.Top), top)]
	for _, b := range system {
		available, err := decimal.NewFromString(b.Available)
		if err != nil {
			return ports.Holdings{}, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "ledger-service answered a balance in another shape")
		}
		frozen, err := decimal.NewFromString(b.Frozen)
		if err != nil {
			return ports.Holdings{}, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "ledger-service answered a balance in another shape")
		}
		if sum := available.Add(frozen); !sum.IsZero() {
			out.System[b.AccountType] = out.System[b.AccountType].Add(sum)
		}
	}
	return out, nil
}

// SimMintInput is more of the coin or USDT for the bots: Amount in all,
// spread evenly over the bots of Role (every bot when empty).
type SimMintInput struct {
	Asset  string
	Amount decimal.Decimal
	Role   string
	Reason string
	// Reference is an optional ticket kept with it.
	Reference string
	// Key is the request's Idempotency-Key ("" for none).
	Key string
}

// MintShare is one bot's part of a mint.
type MintShare struct {
	UserID string `json:"user_id"`
	Label  string `json:"label"`
	Amount string `json:"amount"`
}

// mintScale is the decimal places of a bot's share (the bots' assets have
// more); the first bot takes what the rounding leaves.
const mintScale = 2

// ErrNoBots refuses a mint without bots to share it.
var ErrNoBots = apperr.New(apperr.KindUnprocessable, "ADMIN_SIM_NO_BOTS", "no bots to share it among")

// The most one mint books, whoever approves it (admin console C5.5 ③):
// of the coin, and of USDT.
const (
	mintCapCoin = 10_000_000
	mintCapUSDT = 1_000_000
)

// ErrMintCap refuses a mint beyond its cap (details asset, max).
var ErrMintCap = apperr.New(apperr.KindUnprocessable, "ADMIN_SIM_MINT_CAP", "more than one mint may book")

// MintSimBots books more of the coin or USDT for the simulated market's
// bots, in their SPOT accounts, as one fund operation: carried out at
// once within the single-person limits, or waiting for a second
// administrator, like any adjustment and with the same permissions.
func (s *Service) MintSimBots(ctx context.Context, p Principal, in SimMintInput) (domain.Approval, error) {
	if err := p.require(domain.PermAdjustRequest); err != nil {
		return domain.Approval{}, err
	}
	if err := needReason(in.Reason); err != nil {
		return domain.Approval{}, err
	}
	if !in.Amount.IsPositive() {
		return domain.Approval{}, apperr.Invalid("the amount must be positive")
	}
	st, err := s.simState(ctx)
	if err != nil {
		return domain.Approval{}, err
	}
	asset := strings.ToUpper(strings.TrimSpace(in.Asset))
	if asset != st.coin() && asset != "USDT" {
		return domain.Approval{}, apperr.Invalid(fmt.Sprintf("the bots hold %s and USDT", st.coin()))
	}
	most := decimal.NewFromInt(mintCapCoin)
	if asset == "USDT" {
		most = decimal.NewFromInt(mintCapUSDT)
	}
	if in.Amount.GreaterThan(most) {
		return domain.Approval{}, ErrMintCap.WithDetail("asset", asset).WithDetail("max", most.String())
	}
	role := strings.ToUpper(strings.TrimSpace(in.Role))
	var bots []simBot
	for _, b := range st.Bots {
		if role == "" || b.Role == role {
			bots = append(bots, b)
		}
	}
	shares, err := splitMint(in.Amount, bots)
	if err != nil {
		return domain.Approval{}, err
	}
	return s.SubmitFunds(ctx, p, FundRequest{
		Kind: domain.KindSimMint, Asset: asset, Amount: in.Amount, Reason: in.Reason, Reference: in.Reference, Shares: shares, Role: role,
		Direct: true, Key: in.Key,
	})
}

// splitMint shares an amount evenly among the bots, to mintScale places;
// the first takes the rest.
func splitMint(amount decimal.Decimal, bots []simBot) ([]MintShare, error) {
	if len(bots) == 0 {
		return nil, ErrNoBots
	}
	n := decimal.NewFromInt(int64(len(bots)))
	each := amount.Div(n).Truncate(mintScale)
	if !each.IsPositive() {
		return nil, apperr.Invalid(fmt.Sprintf("too little to share among %d bots", len(bots)))
	}
	first := amount.Sub(each.Mul(n.Sub(decimal.NewFromInt(1))))
	out := make([]MintShare, 0, len(bots))
	for i, b := range bots {
		share := each
		if i == 0 {
			share = first
		}
		out = append(out, MintShare{UserID: b.UserID, Label: b.Label, Amount: share.String()})
	}
	return out, nil
}

// mintBots books an approved mint: one adjustment per bot under the key
// approval:<id>:<user>, so a second attempt books only what the first did
// not. An error leaves the outcome unknown (it stays pending). A refusal
// of the first bot fails it, nothing booked; a refusal after some were
// booked keeps it pending (and counted in its requester's 24 hours) to be
// finished once the cause is fixed, the error saying how far it got.
func (s *Service) mintBots(ctx context.Context, a *domain.Approval, p Principal) error {
	var shares []MintShare
	if err := json.Unmarshal([]byte(a.Payload["bots"]), &shares); err != nil {
		return err
	}
	note := a.Reason
	if ref := a.Payload["reference"]; ref != "" {
		note += " [" + ref + "]"
	}
	journals := make([]string, 0, len(shares))
	for _, sh := range shares {
		amount, err := decimal.NewFromString(sh.Amount)
		if err != nil {
			return err
		}
		journal, err := s.Ledger.Adjust(ctx, "approval:"+a.ID+":"+sh.UserID, sh.UserID, AccountSpot, a.Payload["asset"], amount, p.Admin.Email, note)
		if err != nil {
			var e *apperr.Error
			if !errors.As(err, &e) || e.Kind == apperr.KindUnavailable || e.Kind == apperr.KindInternal {
				return err
			}
			if len(journals) > 0 {
				return e.WithDetail("bot", sh.Label).WithDetail("booked", len(journals)).WithDetail("of", len(shares))
			}
			a.Status, a.Result = domain.ApprovalFailed, fmt.Sprintf("%s: %s: %s", sh.Label, e.Code, e.Message)
			return nil
		}
		journals = append(journals, journal)
	}
	if len(journals) == 0 {
		return ErrNoBots
	}
	a.Status, a.JournalID = domain.ApprovalExecuted, journals[0]
	a.Result = fmt.Sprintf("%d adjustments, journals %s to %s", len(journals), journals[0], journals[len(journals)-1])
	return nil
}
