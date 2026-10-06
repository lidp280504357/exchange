package application

import (
	"context"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	marginv1 "github.com/skill/exchange/api/gen/go/exchange/margin/v1"
	"github.com/skill/exchange/internal/margin/domain"
	"github.com/skill/exchange/internal/margin/ports"
	"github.com/skill/exchange/internal/platform/event"
)

// Monitor watches the margin levels (design §4.4, §4.5) and publishes the
// margin accounts as they change (the private channel margin's ACCOUNT
// pushes, design §5.2). Each pass values the accounts that owe anything
// or are not NORMAL with the latest prices: one that fell under its
// warning level is WARNED (MarginLevelWarned, once until it is back above
// the line), one back above it NORMAL again; one at its liquidation level
// two passes in a row goes to Liquidate. An account whose valuation is not
// complete (an asset never priced) keeps its state. A user's holdings are
// read again when the ledger or a write touched them (Touch) and at least
// every Refresh.
type Monitor struct {
	Svc    *Service
	Pushes ports.Pushes
	// Refresh bounds how long a user's holdings serve; Track how long the
	// list of the accounts to watch does.
	Refresh time.Duration
	Track   time.Duration
	// Liquidate starts an account's liquidation (batch E3); nil only
	// counts the account as due.
	Liquidate func(ctx context.Context, st ports.Account, v domain.Valuation) error

	mu      sync.Mutex
	touched map[string]bool
	held    map[string]heldAt
	users   map[string][]ports.Account
	tracked time.Time
	hits    map[accountOf]int
	sent    map[accountOf]sentAccount
	// waitLogged is when a waiting liquidation was last logged: once a
	// minute, not every pass (review DD).
	waitLogged time.Time
}

type heldAt struct {
	accounts map[domain.Account][]domain.Holding
	at       time.Time
}

// accountOf is one user's account.
type accountOf struct {
	user    string
	account domain.Account
}

// sentAccount is what the last push of an account said.
type sentAccount struct {
	at     time.Time
	status domain.Status
	level  decimal.Decimal
	asset  decimal.Decimal
	debt   decimal.Decimal
}

// pushEvery bounds the pushes of an account; pushMove is the relative
// change of its level or totals that makes the next one.
const (
	pushEvery = time.Second
	pushMove  = "0.001"
)

// Touch marks a user whose margin accounts changed: the next pass reads
// their holdings again and publishes the accounts.
func (m *Monitor) Touch(userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.touched == nil {
		m.touched = map[string]bool{}
	}
	m.touched[userID] = true
}

func (m *Monitor) takeTouched() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.touched
	m.touched = map[string]bool{}
	return t
}

// Pass values the watched accounts once; it returns how many it valued.
func (m *Monitor) Pass(ctx context.Context) (int, error) {
	s := m.Svc
	now := s.Now()
	touched := m.takeTouched()
	if m.held == nil {
		m.held, m.hits, m.sent = map[string]heldAt{}, map[accountOf]int{}, map[accountOf]sentAccount{}
	}
	r := s.Store.Read()
	if now.Sub(m.tracked) >= m.Track || m.users == nil {
		states, err := r.Accounts().WithDebt(ctx)
		if err != nil {
			m.retouch(touched)
			return 0, err
		}
		m.users = map[string][]ports.Account{}
		for _, st := range states {
			m.users[st.UserID] = append(m.users[st.UserID], st)
		}
		m.tracked = now
	}
	// The touched users' accounts, owing or not, and their state now.
	users := make(map[string][]ports.Account, len(m.users)+len(touched))
	for u, list := range m.users {
		users[u] = list
	}
	for u := range touched {
		list, err := r.Accounts().OfUser(ctx, u)
		if err != nil {
			m.retouch(touched)
			return 0, err
		}
		users[u] = list
	}
	cat, err := s.catalog(ctx, r)
	if err != nil {
		m.retouch(touched)
		return 0, err
	}
	prices := s.Prices.Prices()
	var pushes []*marginv1.MarginAccountUpdated
	seen := map[accountOf]bool{}
	n := 0
	for user, list := range users {
		h, ok := m.held[user]
		if !ok || touched[user] || now.Sub(h.at) >= m.Refresh {
			all, err := s.Ledger.Holdings(ctx, user)
			if err != nil { // this user waits for the next pass, the others go on (review CY C17 ③)
				s.Log.WarnContext(ctx, "margin monitor: holdings not read", "user_id", user, "error", err)
				if touched[user] {
					m.Touch(user)
				}
				continue
			}
			h = heldAt{accounts: all, at: now}
			m.held[user] = h
		}
		for i, st := range list {
			key := accountOf{user: user, account: st.Account}
			seen[key] = true
			n++
			holdings := h.accounts[st.Account]
			v := domain.Value(holdings, cat.Assets, prices)
			if st, err = m.judge(ctx, cat, st, v); err != nil {
				s.Log.WarnContext(ctx, "margin level check failed", "user_id", user, "account", st.Account.Key(), "error", err)
			}
			list[i] = st // the state as it stands, until the list is read again
			if m.push(key, st, v, touched[user], now) {
				pushes = append(pushes, accountUpdate(s.view(ctx, cat, prices, st, holdings), user, now))
			}
		}
	}
	for key := range m.sent {
		if !seen[key] {
			delete(m.sent, key)
			delete(m.hits, key)
		}
	}
	for user := range m.held {
		if _, ok := users[user]; !ok {
			delete(m.held, user)
		}
	}
	if _, err := s.AdvanceLiquidations(ctx); err != nil && now.Sub(m.waitLogged) >= time.Minute {
		m.waitLogged = now
		s.Log.WarnContext(ctx, "a liquidation waits", "error", err)
	}
	if len(pushes) > 0 && m.Pushes != nil {
		if err := m.Pushes.Push(ctx, pushes); err != nil {
			s.Log.WarnContext(ctx, "margin account pushes failed; the next change sends them again", "accounts", len(pushes), "error", err)
		}
	}
	if s.Metrics != nil {
		s.Metrics.Watched.Set(float64(n))
		s.Metrics.MonitorPass.SetToCurrentTime()
	}
	return n, nil
}

// retouch puts back the users a failed pass took.
func (m *Monitor) retouch(users map[string]bool) {
	for u := range users {
		m.Touch(u)
	}
}

// judge moves an account's state with its margin level and returns it as
// it stands.
func (m *Monitor) judge(ctx context.Context, cat Catalog, st ports.Account, v domain.Valuation) (ports.Account, error) {
	key := accountOf{user: st.UserID, account: st.Account}
	terms, err := cat.Terms(st.Account)
	if err != nil {
		terms = domain.DefaultTerms(st.Account.Type, 3)
	}
	if !v.Complete() || st.Status == domain.StatusLiquidating {
		delete(m.hits, key)
		return st, nil
	}
	zone := terms.ZoneOf(v)
	if zone != domain.ZoneLiquidate {
		delete(m.hits, key)
	} else if m.hits[key]++; m.hits[key] >= 2 {
		if m.hits[key] == 2 && m.Svc.Metrics != nil { // once each time it reaches the line (review CY C17 ④)
			m.Svc.Metrics.LiquidationsDue.Inc()
		}
		if m.Liquidate != nil {
			if err := m.Liquidate(ctx, st, v); err != nil {
				return st, err
			}
			if cur, ok, err := m.Svc.Store.Read().Accounts().Get(ctx, st.UserID, st.Account); err == nil && ok {
				st = cur // LIQUIDATING once it started
			}
			if st.Status == domain.StatusLiquidating {
				return st, nil
			}
		}
	}
	// Back above the warning line by a hundredth of it, not at the line
	// itself: a level that wavers around it warns once (review CY).
	level, _ := v.Level()
	recovered := zone == domain.ZoneSafe && !level.LessThan(terms.WarnLevel.Mul(decimal.RequireFromString("1.01")))
	switch {
	case recovered && st.Status == domain.StatusWarned:
		return m.setStatus(ctx, st, domain.StatusWarned, domain.StatusNormal, terms, v)
	case zone != domain.ZoneSafe && st.Status == domain.StatusNormal:
		return m.setStatus(ctx, st, domain.StatusNormal, domain.StatusWarned, terms, v)
	}
	return st, nil
}

// setStatus moves an account from one status to another under the user's
// lock, unless something else moved it meanwhile; a warning queues
// MarginLevelWarned.
func (m *Monitor) setStatus(ctx context.Context, st ports.Account, from, to domain.Status, terms domain.Terms, v domain.Valuation) (ports.Account, error) {
	s := m.Svc
	out := st
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.LockUser(ctx, st.UserID); err != nil {
			return err
		}
		cur, ok, err := r.Accounts().Get(ctx, st.UserID, st.Account)
		if err != nil || !ok || cur.Status != from {
			if ok {
				out = cur
			}
			return err
		}
		now := s.Now()
		cur.Status, cur.UpdatedAt = to, now
		if to == domain.StatusWarned {
			cur.WarnedAt = now
		} else {
			cur.WarnedAt = time.Time{}
		}
		if err := r.Accounts().Update(ctx, cur); err != nil {
			return err
		}
		out = cur
		if to != domain.StatusWarned {
			return nil
		}
		level, _ := v.Level()
		if s.Metrics != nil {
			s.Metrics.Warned.Inc()
		}
		return r.Emit(ctx, event.TopicMargin, &marginv1.MarginLevelWarned{
			UserId: st.UserID, AccountType: string(st.Account.Type), Symbol: st.Account.Symbol, MarginLevel: level.String(),
			WarnLevel: terms.WarnLevel.String(), LiquidationLevel: terms.LiquidationLevel.String(), TotalAsset: v.TotalAsset.String(),
			TotalLiability: v.TotalLiability.String(), WarnedAt: timestamppb.New(now),
		}, "user", st.UserID)
	})
	return out, err
}

// push decides whether an account is published now: when its user's
// holdings or its status changed, or its margin level or totals moved by
// pushMove — at most once every pushEvery.
func (m *Monitor) push(key accountOf, st ports.Account, v domain.Valuation, touched bool, now time.Time) bool {
	level, _ := v.Level()
	last, ok := m.sent[key]
	moved := func(a, b decimal.Decimal) bool {
		if b.IsZero() {
			return !a.IsZero()
		}
		return a.Sub(b).Abs().Div(b.Abs()).GreaterThan(decimal.RequireFromString(pushMove))
	}
	due := !ok || touched || last.status != st.Status || moved(level, last.level) || moved(v.TotalAsset, last.asset) ||
		moved(v.TotalLiability, last.debt)
	if !due || (ok && now.Sub(last.at) < pushEvery && !touched && last.status == st.Status) {
		return false
	}
	m.sent[key] = sentAccount{at: now, status: st.Status, level: level, asset: v.TotalAsset, debt: v.TotalLiability}
	return true
}

// accountUpdate renders an account for margin.accounts.
func accountUpdate(v AccountView, userID string, now time.Time) *marginv1.MarginAccountUpdated {
	out := &marginv1.MarginAccountUpdated{
		UserId: userID, AccountType: string(v.Account.Type), Symbol: v.Account.Symbol, Leverage: int32(v.Terms.Leverage), //nolint:gosec // 2 to 10
		Status: string(v.Status), WarnLevel: v.Terms.WarnLevel.String(), LiquidationLevel: v.Terms.LiquidationLevel.String(),
		TotalAsset: v.Valuation.TotalAsset.String(), TotalLiability: v.Valuation.TotalLiability.String(),
		NetAsset: v.Valuation.Net().String(), UpdatedAt: timestamppb.New(now),
	}
	if out.Status == "" {
		out.Status = string(domain.StatusNormal)
	}
	if level, ok := v.Valuation.Level(); ok {
		out.MarginLevel = level.String()
	}
	if v.LiquidationPrice != nil {
		out.LiquidationPrice = v.LiquidationPrice.String()
	}
	for _, h := range v.Holdings {
		if h.Empty() {
			continue
		}
		out.Balances = append(out.Balances, &marginv1.MarginBalance{
			Asset: h.Asset, Free: h.Free.String(), Locked: h.Locked.String(), Borrowed: h.Borrowed.String(),
			Interest: h.Interest.String(), Net: h.Net().String(),
		})
	}
	return out
}
