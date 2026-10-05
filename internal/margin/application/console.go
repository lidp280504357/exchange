package application

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The admin console's side of margin-service (coordination decisions of
// 2026-10-06 03:24 ⑤ and ⑥, review C5). admin-service keeps the
// approvals and the audit and sends what an administrator decided;
// margin-service applies it over the version read, after its own policy:
// the leverage sets, the thresholds, the rates and caps.

// The leverages the console may set (decision ⑤); the terms themselves
// take 2 to MaxLeverage.
var (
	CrossLeverages    = []int{3, 5}
	IsolatedLeverages = []int{3, 5, 10}
)

// ErrParamsChanged refuses a change of terms whose version moved since
// they were read.
var ErrParamsChanged = apperr.New(apperr.KindConflict, "MARGIN_PARAMS_CHANGED", "the terms changed since they were read")

// ErrNotFrozen refuses to unfreeze an account no administrator froze (a
// liquidation's freeze ends with the liquidation).
var ErrNotFrozen = apperr.New(apperr.KindConflict, "MARGIN_NOT_FROZEN", "the account is not frozen by an administrator")

// ErrAccountNotFound answers for an account the user never opened.
var ErrAccountNotFound = apperr.New(apperr.KindNotFound, "MARGIN_ACCOUNT_NOT_FOUND", "no such margin account")

// ConsoleAsset is an asset's terms with their version and what users owe
// of it now.
type ConsoleAsset struct {
	Terms domain.AssetTerms
	Meta  ports.Meta
	// Lent and InterestOwed are what users owe (the loans), Borrowers the
	// accounts that owe it.
	Lent         decimal.Decimal
	InterestOwed decimal.Decimal
	Borrowers    int
	// Utilization is Lent over the pool's cap; Rate the hour's rate.
	Utilization decimal.Decimal
	Rate        decimal.Decimal
	RateHour    time.Time
}

// ConsoleAssets lists the margin list's assets for the console.
func (s *Service) ConsoleAssets(ctx context.Context) ([]ConsoleAsset, error) {
	r := s.Store.Read()
	assets, err := r.Terms().Assets(ctx)
	if err != nil {
		return nil, err
	}
	metas, err := r.Terms().AssetMetas(ctx)
	if err != nil {
		return nil, err
	}
	totals, err := r.Loans().Totals(ctx)
	if err != nil {
		return nil, err
	}
	hour := domain.Hour(s.Now())
	out := make([]ConsoleAsset, 0, len(assets))
	for _, t := range assets {
		owed := totals[t.Asset]
		c := ConsoleAsset{
			Terms: t, Meta: metas[t.Asset], Lent: owed.Principal, InterestOwed: owed.Interest, Borrowers: owed.Borrowers,
			Utilization: domain.Use(owed.Principal, t.PoolCap).Round(6), RateHour: hour,
		}
		if c.Lent.IsZero() {
			c.Lent, c.InterestOwed = decimal.Zero, decimal.Zero
		}
		if c.Rate, err = s.currentRate(ctx, r, t, hour, owed.Principal); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// consoleAsset returns one asset as ConsoleAssets lists it.
func (s *Service) consoleAsset(ctx context.Context, asset string) (ConsoleAsset, error) {
	all, err := s.ConsoleAssets(ctx)
	if err != nil {
		return ConsoleAsset{}, err
	}
	for _, a := range all {
		if a.Terms.Asset == asset {
			return a, nil
		}
	}
	return ConsoleAsset{}, apperr.NotFound("no such margin asset")
}

// checkPrecision refuses a value with more decimals than its column keeps.
func checkPrecision(name string, v decimal.Decimal, decimals int32) error {
	if !v.Equal(v.Truncate(decimals)) {
		return apperr.Invalid(fmt.Sprintf("%s has more than %d decimals", name, decimals))
	}
	return nil
}

// SetAssetTerms stores an asset's terms over the version read (0 adds an
// asset the instruments know): the haircut above 0 and at most 1, the
// user cap within the pool cap, the rates not negative and the floating
// curve rising whichever model is in force.
func (s *Service) SetAssetTerms(ctx context.Context, t domain.AssetTerms, version int64, by string) (ConsoleAsset, error) {
	decimals, err := s.Instruments.Decimals(ctx, t.Asset)
	if err != nil {
		return ConsoleAsset{}, err
	}
	t.Decimals = decimals
	if err := t.Validate(); err != nil {
		return ConsoleAsset{}, err
	}
	if err := t.Floating.Validate(); err != nil {
		return ConsoleAsset{}, apperr.Invalid(t.Asset + ": " + apperr.From(err).Message)
	}
	if t.FixedRate.IsNegative() {
		return ConsoleAsset{}, apperr.Invalid(t.Asset + ": the fixed rate must not be negative")
	}
	for _, c := range []struct {
		name     string
		v        decimal.Decimal
		decimals int32
	}{
		{"haircut", t.Haircut, 4},
		{"float_kink", t.Floating.Kink, 4},
		{"fixed_rate", t.FixedRate, domain.RateDecimals},
		{"float_base", t.Floating.Base, domain.RateDecimals},
		{"float_kink_rate", t.Floating.KinkRate, domain.RateDecimals},
		{"float_max_rate", t.Floating.MaxRate, domain.RateDecimals},
		{"pool_cap", t.PoolCap, 18},
		{"user_cap", t.UserCap, 18},
	} {
		if err := checkPrecision(c.name, c.v, c.decimals); err != nil {
			return ConsoleAsset{}, err
		}
	}
	if err := s.saveIf(ctx, func(r ports.Repos) (bool, error) { return r.Terms().SaveAssetIf(ctx, t, by, version) }); err != nil {
		return ConsoleAsset{}, err
	}
	return s.consoleAsset(ctx, t.Asset)
}

// saveIf runs a conditional save: a moved version is MARGIN_PARAMS_CHANGED.
func (s *Service) saveIf(ctx context.Context, save func(ports.Repos) (bool, error)) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		ok, err := save(r)
		if err != nil {
			return err
		}
		if !ok {
			return ErrParamsChanged
		}
		return nil
	})
}

// checkTerms checks an account's terms against the policy: the leverage
// of the set, the levels and the fee as the terms take them, each within
// its column's decimals.
func checkTerms(t domain.Terms, leverages []int) error {
	if !slices.Contains(leverages, t.Leverage) {
		return apperr.Invalid(fmt.Sprintf("the leverage is one of %v", leverages))
	}
	if err := t.Validate(); err != nil {
		return err
	}
	if err := checkPrecision("warn_level", t.WarnLevel, 4); err != nil {
		return err
	}
	if err := checkPrecision("liquidation_level", t.LiquidationLevel, 4); err != nil {
		return err
	}
	return checkPrecision("liquidation_fee", t.LiquidationFee, 6)
}

// ConsoleSettings is the cross account's terms with their version and the
// design's thresholds by isolated leverage, which the console offers when
// a pair's leverage changes.
type ConsoleSettings struct {
	Cross            domain.Terms
	Meta             ports.Meta
	IsolatedDefaults []domain.Terms
}

// ConsoleSettings returns the cross account's terms for the console.
func (s *Service) ConsoleSettings(ctx context.Context) (ConsoleSettings, error) {
	r := s.Store.Read()
	cross, ok, err := r.Terms().Cross(ctx)
	if err != nil {
		return ConsoleSettings{}, err
	}
	if !ok {
		cross = domain.DefaultTerms(domain.AccountCross, 3)
	}
	meta, _, err := r.Terms().CrossMeta(ctx)
	if err != nil {
		return ConsoleSettings{}, err
	}
	out := ConsoleSettings{Cross: cross, Meta: meta}
	for _, l := range IsolatedLeverages {
		out.IsolatedDefaults = append(out.IsolatedDefaults, domain.DefaultTerms(domain.AccountIsolated, l))
	}
	return out, nil
}

// SetCrossTerms stores the cross account's terms over the version read.
func (s *Service) SetCrossTerms(ctx context.Context, t domain.Terms, version int64, by string) (ConsoleSettings, error) {
	if err := checkTerms(t, CrossLeverages); err != nil {
		return ConsoleSettings{}, err
	}
	if err := s.saveIf(ctx, func(r ports.Repos) (bool, error) { return r.Terms().SaveCrossIf(ctx, t, by, version) }); err != nil {
		return ConsoleSettings{}, err
	}
	return s.ConsoleSettings(ctx)
}

// ConsolePair is a pair's isolated terms with their version and how many
// isolated accounts it has.
type ConsolePair struct {
	Pair     domain.Pair
	Meta     ports.Meta
	Accounts int
}

// ConsolePairs lists the pairs' isolated terms for the console.
func (s *Service) ConsolePairs(ctx context.Context) ([]ConsolePair, error) {
	r := s.Store.Read()
	pairs, err := r.Terms().Pairs(ctx)
	if err != nil {
		return nil, err
	}
	metas, err := r.Terms().PairMetas(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := r.Accounts().IsolatedCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ConsolePair, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, ConsolePair{Pair: p, Meta: metas[p.Symbol], Accounts: counts[p.Symbol]})
	}
	return out, nil
}

// SetPairTerms stores a pair's isolated terms over the version read (0
// adds a pair the instruments list whose two assets are margin assets).
func (s *Service) SetPairTerms(ctx context.Context, p domain.Pair, version int64, by string) (ConsolePair, error) {
	if err := checkTerms(p.Terms, IsolatedLeverages); err != nil {
		return ConsolePair{}, err
	}
	info, err := s.Instruments.Pair(ctx, p.Symbol)
	if err != nil {
		return ConsolePair{}, err
	}
	p.Base, p.Quote = info.Base, info.Quote
	r := s.Store.Read()
	for _, asset := range []string{p.Base, p.Quote} {
		if _, ok, err := r.Terms().Asset(ctx, asset); err != nil {
			return ConsolePair{}, err
		} else if !ok {
			return ConsolePair{}, domain.ErrNotBorrowable.WithDetail("asset", asset)
		}
	}
	if err := s.saveIf(ctx, func(r ports.Repos) (bool, error) { return r.Terms().SavePairIf(ctx, p, by, version) }); err != nil {
		return ConsolePair{}, err
	}
	all, err := s.ConsolePairs(ctx)
	if err != nil {
		return ConsolePair{}, err
	}
	for _, c := range all {
		if c.Pair.Symbol == p.Symbol {
			return c, nil
		}
	}
	return ConsolePair{}, fmt.Errorf("pair terms of %s vanished", p.Symbol)
}

// ConsoleAccount is a margin account as the console lists it.
type ConsoleAccount struct {
	State ports.Account
	View  AccountView
	// Unpriced lists the assets it holds or owes without a fresh price.
	Unpriced []string
}

// empty reports whether the account holds and owes nothing.
func (c ConsoleAccount) empty() bool {
	return !slices.ContainsFunc(c.View.Holdings, func(h domain.Holding) bool { return !h.Empty() })
}

// consoleScan bounds the accounts a list values.
const consoleScan = 5000

// ConsoleAccounts lists the accounts the filter matches that hold or owe
// anything, riskiest first: the lowest margin level first, the accounts
// without liabilities after them by net assets; at most limit, truncated
// when there are more.
func (s *Service) ConsoleAccounts(ctx context.Context, f ports.AccountFilter, limit int) ([]ConsoleAccount, bool, error) {
	r := s.Store.Read()
	states, err := r.Accounts().List(ctx, f, consoleScan+1)
	if err != nil {
		return nil, false, err
	}
	truncated := len(states) > consoleScan
	if truncated {
		states = states[:consoleScan]
	}
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return nil, false, err
	}
	prices := s.Prices.Prices()
	held := map[string]map[domain.Account][]domain.Holding{}
	var out []ConsoleAccount
	for _, st := range states {
		all, ok := held[st.UserID]
		if !ok {
			if all, err = s.Ledger.Holdings(ctx, st.UserID); err != nil {
				return nil, false, err
			}
			held[st.UserID] = all
		}
		c := s.consoleAccount(ctx, cat, prices, st, all[st.Account])
		if !c.empty() {
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, riskiestFirst)
	if len(out) > limit {
		out, truncated = out[:limit], true
	}
	return out, truncated, nil
}

// riskiestFirst orders accounts by margin level, those without
// liabilities last by net assets.
func riskiestFirst(a, b ConsoleAccount) int {
	la, oka := a.View.Valuation.Level()
	lb, okb := b.View.Valuation.Level()
	switch {
	case oka && okb:
		return la.Cmp(lb)
	case oka:
		return -1
	case okb:
		return 1
	}
	return cmp.Compare(0, a.View.Valuation.Net().Cmp(b.View.Valuation.Net()))
}

func (s *Service) consoleAccount(ctx context.Context, cat Catalog, prices domain.Prices, st ports.Account, holdings []domain.Holding) ConsoleAccount {
	c := ConsoleAccount{State: st, View: s.view(ctx, cat, prices, st, holdings), Unpriced: []string{}}
	for _, h := range holdings {
		if h.Empty() {
			continue
		}
		if p, ok := prices.Of(h.Asset); !ok || !p.Fresh {
			c.Unpriced = append(c.Unpriced, h.Asset)
		}
	}
	slices.Sort(c.Unpriced)
	return c
}

// ConsoleBalance is an asset a margin account holds or owes, with its
// worth.
type ConsoleBalance struct {
	Holding domain.Holding
	// Price is the asset's price in USDT, the last known one when it is
	// not fresh; ok is false when it never had one.
	Price   decimal.Decimal
	Priced  bool
	Haircut decimal.Decimal
	Rate    decimal.Decimal
}

// ConsoleAccountDetail is an account with its balances, loans, latest
// loan changes and interest charges.
type ConsoleAccountDetail struct {
	ConsoleAccount
	Balances []ConsoleBalance
	Loans    []LoanView
	Changes  []ports.LoanChange
	Interest []ports.Charge
}

// consoleList bounds the loan changes and charges an account shows.
const consoleList = 50

// ConsoleAccountDetail returns one account in full.
func (s *Service) ConsoleAccountDetail(ctx context.Context, userID string, a domain.Account) (ConsoleAccountDetail, error) {
	r := s.Store.Read()
	st, ok, err := r.Accounts().Get(ctx, userID, a)
	if err != nil {
		return ConsoleAccountDetail{}, err
	}
	if !ok {
		return ConsoleAccountDetail{}, ErrAccountNotFound
	}
	cat, err := s.catalog(ctx, r)
	if err != nil {
		return ConsoleAccountDetail{}, err
	}
	all, err := s.Ledger.Holdings(ctx, userID)
	if err != nil {
		return ConsoleAccountDetail{}, err
	}
	prices := s.Prices.Prices()
	d := ConsoleAccountDetail{ConsoleAccount: s.consoleAccount(ctx, cat, prices, st, all[a])}
	if d.Loans, err = s.Loans(ctx, userID, &a); err != nil {
		return ConsoleAccountDetail{}, err
	}
	rates := map[string]decimal.Decimal{}
	for _, l := range d.Loans {
		rates[l.Loan.Asset] = l.Rate
	}
	for _, h := range d.View.Holdings {
		b := ConsoleBalance{Holding: h, Haircut: decimal.Zero, Rate: rates[h.Asset]}
		if b.Rate.IsZero() {
			b.Rate = decimal.Zero
		}
		if t, ok := cat.Assets[h.Asset]; ok && t.Collateral {
			b.Haircut = t.Haircut
		}
		if p, ok := prices.Of(h.Asset); ok {
			b.Price, b.Priced = p.Value, true
		}
		d.Balances = append(d.Balances, b)
	}
	if d.Changes, err = r.Loans().Changes(ctx, userID, a, consoleList); err != nil {
		return ConsoleAccountDetail{}, err
	}
	if d.Interest, err = r.Interest().OfAccount(ctx, userID, a, consoleList); err != nil {
		return ConsoleAccountDetail{}, err
	}
	return d, nil
}

// Freeze freezes a margin account for an administrator (decision ⑤):
// no borrowing, transfers out or orders until it is unfrozen; repaying
// stays open, interest goes on and the account is still liquidated at its
// liquidation level. A frozen or liquidating account answers MARGIN_FROZEN.
func (s *Service) Freeze(ctx context.Context, userID string, a domain.Account, by, reason string) (ConsoleAccount, error) {
	if by == "" {
		return ConsoleAccount{}, apperr.Invalid("the administrator is required (X-Admin-Id)")
	}
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
		st, ok, err := r.Accounts().Get(ctx, userID, a)
		if err != nil {
			return err
		}
		if !ok {
			return ErrAccountNotFound
		}
		if !st.Status.Open() {
			return domain.ErrFrozen.WithDetail("status", string(st.Status))
		}
		now := s.Now()
		st.Status, st.FrozenBy, st.FrozenAt, st.FrozenReason, st.UpdatedAt = domain.StatusFrozen, by, now, reason, now
		return r.Accounts().Update(ctx, st)
	})
	if err != nil {
		return ConsoleAccount{}, err
	}
	return s.consoleAccountOf(ctx, userID, a)
}

// Unfreeze lifts an administrator's freeze; the account is NORMAL again
// (the margin level monitor warns it again if it is under its warning
// level).
func (s *Service) Unfreeze(ctx context.Context, userID string, a domain.Account, by string) (ConsoleAccount, error) {
	if by == "" {
		return ConsoleAccount{}, apperr.Invalid("the administrator is required (X-Admin-Id)")
	}
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, userID); err != nil {
			return err
		}
		st, ok, err := r.Accounts().Get(ctx, userID, a)
		if err != nil {
			return err
		}
		if !ok {
			return ErrAccountNotFound
		}
		if st.Status != domain.StatusFrozen || st.FrozenBy == "" {
			return ErrNotFrozen.WithDetail("status", string(st.Status))
		}
		st.Status, st.FrozenBy, st.FrozenAt, st.FrozenReason, st.UpdatedAt = domain.StatusNormal, "", time.Time{}, "", s.Now()
		return r.Accounts().Update(ctx, st)
	})
	if err != nil {
		return ConsoleAccount{}, err
	}
	return s.consoleAccountOf(ctx, userID, a)
}

func (s *Service) consoleAccountOf(ctx context.Context, userID string, a domain.Account) (ConsoleAccount, error) {
	d, err := s.ConsoleAccountDetail(ctx, userID, a)
	return d.ConsoleAccount, err
}
