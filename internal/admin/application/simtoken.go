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

// SimTokenHoldings returns who holds the simulated market's coin as the
// ledger holds it now (A123: the read model's lines it once summed expire
// after 15 days): the bots, the users, the test accounts apart, the system
// accounts with HOUSE's, and the largest holders (A125: neither HOUSE nor
// the test accounts among them).
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
	kinds := s.holderKinds(ctx)
	// Up to ten reads of ledger-service, bounded together (A125).
	read, cancel := context.WithTimeout(ctx, coinReadTimeout)
	defer cancel()
	m, err := s.coinBalances(read, st.coin(), bots, kinds.system)
	if err != nil {
		return SimToken{}, err
	}
	h, err := holdingsOf(m, kinds, simTopHolders)
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

// simHolderLimit is how many of the largest holders are read (ListHolders
// takes at most 1,000): with every holder listed (fewer than that), the
// test accounts are told apart from the users.
const simHolderLimit = 1000

// houseRow is the platform row of the system accounts' users (HOUSE).
const houseRow = "HOUSE"

// holderKinds are the accounts the coin's figures tell apart by their kind
// (L0): the system users (HOUSE, in the platform's row) and the test
// accounts; partial when their kinds could not be read.
type holderKinds struct {
	system  []string
	test    map[string]bool
	partial bool
}

func (s *Service) holderKinds(ctx context.Context) holderKinds {
	if s.KindIDs == nil {
		return holderKinds{partial: true}
	}
	system, err := s.KindIDs.IDs(ctx, []string{KindSystem})
	if err != nil {
		s.Log.WarnContext(ctx, "the coin's holders: the accounts' kinds unknown", "error", err)
		return holderKinds{partial: true}
	}
	test, err := s.KindIDs.IDs(ctx, []string{KindTest})
	if err != nil {
		s.Log.WarnContext(ctx, "the coin's holders: the accounts' kinds unknown", "error", err)
		return holderKinds{partial: true}
	}
	out := holderKinds{system: system, test: make(map[string]bool, len(test))}
	for _, id := range test {
		out.test[id] = true
	}
	return out
}

// coinMoment is the coin as one read of the ledger saw it: the holders
// with the bots and the system users apart (at most limit of them listed,
// those owing last), the system users alone, and the system accounts.
type coinMoment struct {
	all         ports.HolderPage
	limit       int
	systemUsers ports.HolderSum
	system      []ports.Balance
	houseApart  map[string]bool
}

// coinBalances reads the coin's holders and system accounts as one moment:
// the holders again after the others, until what they hold together held
// still (a trade's fee moves the coin to FEE_REVENUE between two reads);
// three tries, then the last read stands.
func (s *Service) coinBalances(ctx context.Context, asset string, bots, system []string) (coinMoment, error) {
	apart := append(slices.Clone(bots), system...)
	read := func() (ports.HolderPage, error) { return s.Ledger.Holders(ctx, asset, simHolderLimit, apart) }
	all, err := read()
	if err != nil {
		return coinMoment{}, err
	}
	m := coinMoment{limit: simHolderLimit, houseApart: make(map[string]bool, len(system))}
	for _, id := range system {
		m.houseApart[id] = true
	}
	for range 3 {
		if m.system, err = s.Ledger.SystemBalances(ctx, asset); err != nil {
			return coinMoment{}, err
		}
		if len(system) > 0 {
			house, err := s.Ledger.Holders(ctx, asset, 1, system)
			if err != nil {
				return coinMoment{}, err
			}
			m.systemUsers = house.Apart
		}
		again, err := read()
		if err != nil {
			return coinMoment{}, err
		}
		before, after := all.Others.Amount.Add(all.Apart.Amount), again.Others.Amount.Add(again.Apart.Amount)
		all = again
		if before.Equal(after) {
			break
		}
	}
	m.all = all
	return m, nil
}

// holdingsOf sums the coin's holders: the bots (the apart sum less the
// system users'), the users (the others less the test accounts, told apart
// when every holder was listed - those owing too, who count in the sums
// but not as holders), the system accounts by type with the system users'
// as HOUSE (those not at zero), and the top largest holders above zero but
// HOUSE and the test accounts.
func holdingsOf(m coinMoment, kinds holderKinds, top int) (ports.Holdings, error) {
	out := ports.Holdings{System: map[string]decimal.Decimal{}, Top: []ports.Holder{}, Partial: []string{}}
	bots := m.all.Apart
	bots.Amount, bots.Holders = bots.Amount.Sub(m.systemUsers.Amount), bots.Holders-m.systemUsers.Holders
	out.Bots, out.BotHolders = bots.Amount, uint64(max(bots.Holders, 0)) //nolint:gosec // not below zero
	users := m.all.Others
	switch {
	case kinds.partial:
		out.Partial = append(out.Partial, "kinds")
	case len(m.all.Top) >= m.limit:
		out.Partial = append(out.Partial, "test") // the list was cut: the test accounts stay among the users
	default:
		var test ports.HolderSum
		for _, h := range m.all.Top {
			if kinds.test[h.UserID] {
				test.Amount = test.Amount.Add(h.Amount)
				if h.Amount.IsPositive() {
					test.Holders++
				}
			}
		}
		users.Amount, users.Holders = users.Amount.Sub(test.Amount), users.Holders-test.Holders
		out.Test = &test
	}
	out.Users, out.UserHolders = users.Amount, uint64(max(users.Holders, 0)) //nolint:gosec // not below zero
	for _, b := range m.system {
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
	if !m.systemUsers.Amount.IsZero() {
		out.System[houseRow] = m.systemUsers.Amount
	}
	for _, h := range m.all.Top {
		if len(out.Top) == top || !h.Amount.IsPositive() {
			break // the largest first: those owing last
		}
		if m.houseApart[h.UserID] || kinds.test[h.UserID] {
			continue
		}
		out.Top = append(out.Top, h)
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
