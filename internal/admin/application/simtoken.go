package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
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
// bots, the users, the system accounts and the largest holders.
func (s *Service) SimTokenHoldings(ctx context.Context, p Principal) (SimToken, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return SimToken{}, err
	}
	st, err := s.simState(ctx)
	if err != nil {
		return SimToken{}, err
	}
	bots := make([]string, 0, len(st.Bots))
	for _, b := range st.Bots {
		bots = append(bots, b.UserID)
	}
	h, err := s.Reports.Holdings(ctx, st.coin(), bots, simTopHolders)
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

// SimMintInput is more of the coin or USDT for the bots: Amount in all,
// spread evenly over the bots of Role (every bot when empty).
type SimMintInput struct {
	Asset  string
	Amount decimal.Decimal
	Role   string
	Reason string
	// Reference is an optional ticket kept with it.
	Reference string
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
		Direct: true,
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
// not. An error leaves the outcome unknown (it stays pending); a refusal
// fails it, saying how many were booked before.
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
			a.Status, a.Result = domain.ApprovalFailed, fmt.Sprintf("%s: %s: %s", sh.Label, e.Code, e.Message)
			if len(journals) > 0 {
				a.Result += fmt.Sprintf(" (%d of %d booked before)", len(journals), len(shares))
			}
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
