package application_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/lidp280504357/exchange/internal/derivatives/domain"
	"github.com/lidp280504357/exchange/internal/derivatives/ports"
)

// memStore is an in-memory ports.Store: transactions run one at a time on
// a copy that replaces the state when they succeed. It lets the
// application tests run without PostgreSQL; with TEST_POSTGRES_DSN they
// run on the real store.
type memStore struct {
	mu sync.Mutex
	st memState
}

type memState struct {
	settings  map[string]domain.Settings
	orders    map[string]domain.Order
	positions map[string]domain.Position
	fills     map[string]domain.Fill
	pending   map[string]ports.PendingSettlement
	contracts map[string]ports.ContractState
	rounds    map[string]domain.FundingRound
	payments  map[string]domain.FundingPayment
	warned    map[string]time.Time
	conds     map[string]domain.Conditional
	events    []proto.Message
	runs      int
}

func newMemStore() *memStore {
	return &memStore{st: memState{
		settings: map[string]domain.Settings{}, orders: map[string]domain.Order{}, positions: map[string]domain.Position{},
		fills: map[string]domain.Fill{}, pending: map[string]ports.PendingSettlement{}, contracts: map[string]ports.ContractState{},
		rounds: map[string]domain.FundingRound{}, payments: map[string]domain.FundingPayment{}, warned: map[string]time.Time{},
		conds: map[string]domain.Conditional{},
	}}
}

func (s *memStore) Tx(_ context.Context, fn func(ports.Repos) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := memState{
		settings: maps.Clone(s.st.settings), orders: maps.Clone(s.st.orders), positions: maps.Clone(s.st.positions),
		fills: maps.Clone(s.st.fills), pending: maps.Clone(s.st.pending), contracts: maps.Clone(s.st.contracts),
		rounds: maps.Clone(s.st.rounds), payments: maps.Clone(s.st.payments), warned: maps.Clone(s.st.warned), conds: maps.Clone(s.st.conds),
		events: slices.Clone(s.st.events), runs: s.st.runs,
	}
	if err := fn(memRepos{st: &tx}); err != nil {
		return err
	}
	s.st = tx
	return nil
}

func (s *memStore) Read() ports.Repos { return memRepos{st: &s.st} }

type memRepos struct{ st *memState }

func (r memRepos) LockUser(context.Context, string) error { return nil }
func (r memRepos) Settings() ports.SettingsRepo           { return memSettings(r) }
func (r memRepos) Orders() ports.OrderRepo                { return memOrders(r) }
func (r memRepos) Positions() ports.PositionRepo          { return memPositions(r) }
func (r memRepos) Fills() ports.FillRepo                  { return memFills(r) }
func (r memRepos) Pending() ports.PendingRepo             { return memPending(r) }
func (r memRepos) Contracts() ports.ContractStateRepo     { return memContracts(r) }
func (r memRepos) Runs() ports.RunRepo                    { return memRuns(r) }
func (r memRepos) Funding() ports.FundingRepo             { return memFunding(r) }
func (r memRepos) Cross() ports.CrossRepo                 { return memCross(r) }
func (r memRepos) Conditionals() ports.ConditionalRepo    { return memConds(r) }

func (r memRepos) Emit(_ context.Context, _ string, msg proto.Message, _, _ string) error {
	r.st.events = append(r.st.events, msg)
	return nil
}

type (
	memSettings  memRepos
	memOrders    memRepos
	memPositions memRepos
	memFills     memRepos
	memPending   memRepos
	memContracts memRepos
	memRuns      memRepos
)

func (r memSettings) Get(_ context.Context, userID, symbol string) (*domain.Settings, error) {
	s, ok := r.st.settings[userID+"|"+symbol]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (r memSettings) Save(_ context.Context, s domain.Settings) error {
	r.st.settings[s.UserID+"|"+s.Symbol] = s
	return nil
}

func (r memOrders) Insert(_ context.Context, o domain.Order) error {
	for _, x := range r.st.orders {
		if x.UserID == o.UserID && x.ClientOrderID == o.ClientOrderID {
			return domain.ErrClientIDReused
		}
	}
	r.st.orders[o.ID] = o
	return nil
}

func (r memOrders) Get(_ context.Context, id string) (domain.Order, error) {
	o, ok := r.st.orders[id]
	if !ok {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return o, nil
}

func (r memOrders) GetForUpdate(ctx context.Context, id string) (domain.Order, error) {
	return r.Get(ctx, id)
}

func (r memOrders) ByClientID(_ context.Context, userID, clientOrderID string) (domain.Order, error) {
	for _, o := range r.st.orders {
		if o.UserID == userID && o.ClientOrderID == clientOrderID {
			return o, nil
		}
	}
	return domain.Order{}, domain.ErrOrderNotFound
}

func (r memOrders) Update(_ context.Context, o domain.Order) error {
	r.st.orders[o.ID] = o
	return nil
}

func (r memOrders) sorted(keep func(domain.Order) bool) []domain.Order {
	var out []domain.Order
	for _, o := range r.st.orders {
		if keep(o) {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b domain.Order) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (r memOrders) Active(_ context.Context, userID, symbol string) ([]domain.Order, error) {
	return r.sorted(func(o domain.Order) bool {
		return o.UserID == userID && (symbol == "" || o.Symbol == symbol) && o.Status.Active()
	}), nil
}

func (r memOrders) CountActive(ctx context.Context, userID, symbol string) (int, int, error) {
	all, _ := r.Active(ctx, userID, "")
	on := 0
	for _, o := range all {
		if o.Symbol == symbol {
			on++
		}
	}
	return on, len(all), nil
}

func (r memOrders) Unreleased(_ context.Context, userID string) ([]domain.Order, error) {
	return r.sorted(func(o domain.Order) bool {
		return o.UserID == userID && o.FreezeState == domain.FreezeDone && o.Reserving() && o.Unreleased().IsPositive()
	}), nil
}

func (r memOrders) List(_ context.Context, userID string, f ports.ListFilter) ([]domain.Order, error) {
	list := r.sorted(func(o domain.Order) bool {
		return o.UserID == userID && (f.Symbol == "" || o.Symbol == f.Symbol) &&
			(len(f.Statuses) == 0 || slices.Contains(f.Statuses, o.Status)) && (f.Before == "" || o.ID < f.Before)
	})
	slices.Reverse(list)
	return list[:min(f.Limit, len(list))], nil
}

func (r memOrders) PendingFreeze(_ context.Context, cutoff time.Time, limit int) ([]domain.Order, error) {
	list := r.sorted(func(o domain.Order) bool { return o.FreezeState == domain.FreezePending && o.CreatedAt.Before(cutoff) })
	return list[:min(limit, len(list))], nil
}

func (r memOrders) ToRelease(_ context.Context, cutoff time.Time, limit int) ([]domain.Order, error) {
	list := r.sorted(func(o domain.Order) bool {
		return !o.Released && o.FreezeState == domain.FreezeDone && o.Status.Terminal() && o.UpdatedAt.Before(cutoff)
	})
	return list[:min(limit, len(list))], nil
}

func positionKey(p domain.Position) string { return p.UserID + "|" + p.Symbol + "|" + string(p.Side) }

func (r memPositions) OfUser(_ context.Context, userID, symbol string) ([]domain.Position, error) {
	var out []domain.Position
	for _, p := range r.st.positions {
		if p.UserID == userID && (symbol == "" || p.Symbol == symbol) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b domain.Position) int { return strings.Compare(positionKey(a), positionKey(b)) })
	return out, nil
}

func (r memPositions) Open(_ context.Context, symbol string) ([]domain.Position, error) {
	var out []domain.Position
	for _, p := range r.st.positions {
		if !p.Flat() && (symbol == "" || p.Symbol == symbol) {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b domain.Position) int { return strings.Compare(positionKey(a), positionKey(b)) })
	return out, nil
}

func (r memPositions) Totals(context.Context) (map[string]ports.Totals, error) {
	out := map[string]ports.Totals{}
	for _, p := range r.st.positions {
		t := out[p.Symbol]
		t.NetQty = t.NetQty.Add(p.Qty)
		if p.Qty.IsPositive() {
			t.NetCost = t.NetCost.Add(p.EntryCost)
		} else {
			t.NetCost = t.NetCost.Sub(p.EntryCost)
		}
		out[p.Symbol] = t
	}
	return out, nil
}

func (r memPositions) Save(_ context.Context, p domain.Position) (domain.Position, error) {
	if old, ok := r.st.positions[positionKey(p)]; ok {
		p.ID, p.Version = old.ID, old.Version+1
	} else {
		if p.ID == "" {
			p.ID = uuid.Must(uuid.NewV7()).String()
		}
		p.Version = 1
	}
	r.st.positions[positionKey(p)] = p
	return p, nil
}

func fillKey(trade string, side domain.Side) string { return trade + ":" + string(side) }

func (r memFills) Has(_ context.Context, tradeID string, side domain.Side) (bool, error) {
	_, ok := r.st.fills[fillKey(tradeID, side)]
	return ok, nil
}

func (r memFills) Insert(_ context.Context, f domain.Fill) error {
	r.st.fills[fillKey(f.TradeID, f.Side)] = f
	return nil
}

func (r memFills) SetSettled(_ context.Context, tradeID string, side domain.Side) error {
	f := r.st.fills[fillKey(tradeID, side)]
	f.Settled = true
	r.st.fills[fillKey(tradeID, side)] = f
	return nil
}

func (r memFills) OfUser(_ context.Context, userID, symbol, before string, limit int) ([]domain.Fill, error) {
	var out []domain.Fill
	for _, f := range r.st.fills {
		if f.UserID == userID && (symbol == "" || f.Symbol == symbol) {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b domain.Fill) int {
		if c := b.ExecutedAt.Compare(a.ExecutedAt); c != 0 {
			return c
		}
		return strings.Compare(fillKey(b.TradeID, b.Side), fillKey(a.TradeID, a.Side))
	})
	if before != "" {
		for i, f := range out {
			if fillKey(f.TradeID, f.Side) == before {
				out = out[i+1:]
				break
			}
		}
	}
	return out[:min(limit, len(out))], nil
}

func (r memFills) LastPrice(_ context.Context, symbol string) (decimal.Decimal, error) {
	var last domain.Fill
	for _, f := range r.st.fills {
		if f.Symbol == symbol && f.ExecutedAt.After(last.ExecutedAt) {
			last = f
		}
	}
	return last.Price, nil
}

func (r memPending) Insert(_ context.Context, p ports.PendingSettlement) error {
	if _, ok := r.st.pending[p.IdemKey]; !ok {
		p.Attempts, p.CreatedAt = 1, time.Now()
		r.st.pending[p.IdemKey] = p
	}
	return nil
}

func (r memPending) Due(_ context.Context, limit int) ([]ports.PendingSettlement, error) {
	var out []ports.PendingSettlement
	for _, p := range r.st.pending {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b ports.PendingSettlement) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out[:min(limit, len(out))], nil
}

func (r memPending) Failed(_ context.Context, key, reason string) error {
	p := r.st.pending[key]
	p.Attempts, p.LastError = p.Attempts+1, reason
	r.st.pending[key] = p
	return nil
}

func (r memPending) Delete(_ context.Context, key string) error {
	delete(r.st.pending, key)
	return nil
}

func (r memPending) Count(context.Context) (int, error) { return len(r.st.pending), nil }

func (r memContracts) Get(_ context.Context, symbol string) (*ports.ContractState, error) {
	s, ok := r.st.contracts[symbol]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (r memContracts) Degrade(_ context.Context, symbol, reason string, at time.Time) (bool, error) {
	if s, ok := r.st.contracts[symbol]; ok && s.ReduceOnly {
		return false, nil
	}
	r.st.contracts[symbol] = ports.ContractState{Symbol: symbol, ReduceOnly: true, Reason: reason, Since: at}
	return true, nil
}

func (r memContracts) Lift(_ context.Context, symbol, by string, _ time.Time) (bool, error) {
	s, ok := r.st.contracts[symbol]
	if !ok || !s.ReduceOnly {
		return false, nil
	}
	s.ReduceOnly, s.LiftedBy = false, by
	r.st.contracts[symbol] = s
	return true, nil
}

func (r memContracts) All(context.Context) ([]ports.ContractState, error) {
	return slices.Collect(maps.Values(r.st.contracts)), nil
}

func (r memRuns) Record(context.Context, time.Time, string, int, []byte) error {
	r.st.runs++
	return nil
}

type memFunding memRepos

func roundKey(symbol string, at time.Time) string { return fmt.Sprintf("%s|%d", symbol, at.Unix()) }

func (r memFunding) Round(_ context.Context, symbol string, at time.Time) (*domain.FundingRound, error) {
	fr, ok := r.st.rounds[roundKey(symbol, at)]
	if !ok {
		return nil, nil
	}
	return &fr, nil
}

func (r memFunding) Snapshot(_ context.Context, fr domain.FundingRound, payments []domain.FundingPayment) error {
	fr.Status = domain.FundingSnapshot
	r.st.rounds[roundKey(fr.Symbol, fr.FundingTime)] = fr
	for _, p := range payments {
		r.st.payments[roundKey(p.Symbol, p.FundingTime)+"|"+p.PositionID] = p
	}
	return nil
}

func (r memFunding) Waiting(context.Context) ([]domain.FundingRound, error) {
	var out []domain.FundingRound
	for _, fr := range r.st.rounds {
		if fr.Status == domain.FundingSnapshot {
			out = append(out, fr)
		}
	}
	slices.SortFunc(out, func(a, b domain.FundingRound) int { return a.FundingTime.Compare(b.FundingTime) })
	return out, nil
}

func (r memFunding) SetRate(_ context.Context, symbol string, at time.Time, rate, mark decimal.Decimal) error {
	fr := r.st.rounds[roundKey(symbol, at)]
	fr.Rate, fr.Mark = rate, mark
	r.st.rounds[roundKey(symbol, at)] = fr
	return nil
}

func (r memFunding) Unsettled(_ context.Context, symbol string, at time.Time) ([]domain.FundingPayment, error) {
	var out []domain.FundingPayment
	for _, p := range r.st.payments {
		if p.Symbol == symbol && p.FundingTime.Equal(at) && !p.Settled {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b domain.FundingPayment) int { return strings.Compare(a.PositionID, b.PositionID) })
	return out, nil
}

func (r memFunding) Settle(_ context.Context, p domain.FundingPayment) error {
	p.Settled, p.SettledAt = true, time.Now()
	r.st.payments[roundKey(p.Symbol, p.FundingTime)+"|"+p.PositionID] = p
	return nil
}

func (r memFunding) Finish(_ context.Context, symbol string, at time.Time, status string) error {
	fr := r.st.rounds[roundKey(symbol, at)]
	fr.Status = status
	r.st.rounds[roundKey(symbol, at)] = fr
	return nil
}

func (r memFunding) OfUser(_ context.Context, userID, symbol, _ string, limit int) ([]domain.FundingPayment, error) {
	var out []domain.FundingPayment
	for _, p := range r.st.payments {
		if p.UserID == userID && (symbol == "" || p.Symbol == symbol) && p.Settled {
			fr := r.st.rounds[roundKey(p.Symbol, p.FundingTime)]
			p.Rate, p.Mark = fr.Rate, fr.Mark
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b domain.FundingPayment) int { return b.FundingTime.Compare(a.FundingTime) })
	return out[:min(limit, len(out))], nil
}

type memCross memRepos

func (r memCross) WarnedAt(_ context.Context, userID string) (time.Time, error) {
	return r.st.warned[userID], nil
}

func (r memCross) SetWarnedAt(_ context.Context, userID string, at time.Time) error {
	r.st.warned[userID] = at
	return nil
}

type memConds memRepos

func (r memConds) Insert(_ context.Context, c domain.Conditional) error {
	r.st.conds[c.ID] = c
	return nil
}

func (r memConds) Get(_ context.Context, id string) (domain.Conditional, error) {
	c, ok := r.st.conds[id]
	if !ok {
		return domain.Conditional{}, domain.ErrOrderNotFound
	}
	return c, nil
}

func (r memConds) Update(_ context.Context, c domain.Conditional) error {
	r.st.conds[c.ID] = c
	return nil
}

func (r memConds) Active(_ context.Context, symbol string) ([]domain.Conditional, error) {
	var out []domain.Conditional
	for _, c := range r.st.conds {
		if c.Status == domain.ConditionalActive && (symbol == "" || c.Symbol == symbol) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b domain.Conditional) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (r memConds) OfUser(_ context.Context, userID, symbol, status, _ string, limit int) ([]domain.Conditional, error) {
	var out []domain.Conditional
	for _, c := range r.st.conds {
		if c.UserID == userID && (symbol == "" || c.Symbol == symbol) && (status == "" || c.Status == status) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b domain.Conditional) int { return strings.Compare(b.ID, a.ID) })
	return out[:min(limit, len(out))], nil
}
