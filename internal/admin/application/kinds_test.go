package application

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// fakeKindIDs answers each kind's accounts, recording what was asked.
type fakeKindIDs struct {
	of    map[string][]string
	asked []string
	// err is user-service not answering.
	err error
}

func (f *fakeKindIDs) IDs(_ context.Context, kinds []string) ([]string, error) {
	f.asked = append(f.asked, strings.Join(kinds, ","))
	if f.err != nil {
		return nil, f.err
	}
	var out []string
	for _, k := range kinds {
		out = append(out, f.of[k]...)
	}
	return out, nil
}

// A list's kinds as accounts (L1): the humans leave the other kinds' out,
// another kind keeps its own, ALL or one account narrows nothing; a
// service's list carries at most MaxKindIDs, the humans' then leaving out
// the bots and HOUSE only.
func TestKindFilter(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ids := &fakeKindIDs{of: map[string][]string{"BOT": {"b1", "b2"}, "TEST": {"t1"}, "SYSTEM": {"s1"}}}
	h.svc.KindIDs = ids
	for name, c := range map[string]struct {
		kinds  []string
		user   string
		only   []string
		except []string
	}{
		"the humans by default": {nil, "", nil, []string{"b1", "b2", "t1", "s1"}},
		"the bots":              {[]string{"BOT"}, "", []string{"b1", "b2"}, nil},
		"humans and tests":      {[]string{"human", "TEST"}, "", nil, []string{"b1", "b2", "s1"}},
		"every kind":            {[]string{"ALL"}, "", nil, nil},
		"all four":              {[]string{"HUMAN", "BOT", "TEST", "SYSTEM"}, "", nil, nil},
		"one account":           {[]string{"BOT"}, someUser, nil, nil},
	} {
		f, err := h.svc.kindFilter(ctx, c.kinds, c.user, false)
		if err != nil || !slices.Equal(f.Only, c.only) || !slices.Equal(f.Except, c.except) || f.Narrowed {
			t.Fatalf("%s: %+v %v", name, f, err)
		}
	}
	// A kind with no account keeps nothing (not every account).
	ids.of["SYSTEM"] = nil
	if f, err := h.svc.kindFilter(ctx, []string{"SYSTEM"}, "", false); err != nil || f.Only == nil || len(f.Only) != 0 || !f.On() {
		t.Fatalf("no account of the kind: %+v %v", f, err)
	}
	if _, err := h.svc.kindFilter(ctx, []string{"ROBOT"}, "", false); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown kind: %v", err)
	}

	// Too many test accounts for a service: the humans leave out the bots
	// and HOUSE only, said; a read model takes them all.
	many := make([]string, MaxKindIDs+1)
	for i := range many {
		many[i] = fmt.Sprintf("t%d", i)
	}
	ids.of["TEST"], ids.of["SYSTEM"] = many, []string{"s1"}
	if f, err := h.svc.kindFilter(ctx, nil, "", true); err != nil || !f.Narrowed || !slices.Equal(f.Except, []string{"b1", "b2", "s1"}) {
		t.Fatalf("narrowed: %+v %v", f, err)
	}
	if f, err := h.svc.kindFilter(ctx, nil, "", false); err != nil || f.Narrowed || len(f.Except) != MaxKindIDs+4 {
		t.Fatalf("a read model's: %d %v %v", len(f.Except), f.Narrowed, err)
	}
	if f, err := h.svc.kindFilter(ctx, []string{"TEST"}, "", true); err != nil || !f.Narrowed || len(f.Only) != MaxKindIDs {
		t.Fatalf("the first test accounts: %d %v %v", len(f.Only), f.Narrowed, err)
	}

	// Without user-service's kinds wired in, every account lists.
	h.svc.KindIDs = nil
	if f, err := h.svc.kindFilter(ctx, nil, "", false); err != nil || f.On() {
		t.Fatalf("no kinds: %+v %v", f, err)
	}
}

// The overview (L1, A108): every kind's account counts, the humans' per
// day (user-service asked twice); the activity is the humans' with each
// other kind's accounts apart; without the kinds, everyone's figures,
// said in partial when they could not be read.
func TestDashboardByKind(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	records := &pageRecords{}
	h.svc.Records = records
	ids := &fakeKindIDs{of: map[string][]string{"BOT": {"b1"}, "TEST": {"t1"}, "SYSTEM": {"s1"}}}
	h.svc.KindIDs = ids
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	d, err := h.svc.Dashboard(ctx, auditor, 7)
	if err != nil || len(d.Partial) != 0 || d.Users.Total != 9 || len(d.Users.Days) != 1 || d.Users.Days["of HUMAN"] != 1 {
		t.Fatalf("dashboard %+v %v", d, err)
	}
	if !slices.Equal(h.users.stats, []string{"", "HUMAN"}) {
		t.Fatalf("stats asked for %q", h.users.stats)
	}
	k := records.activity[0]
	if !slices.Equal(k.Keep.Except, []string{"b1", "t1", "s1"}) || !slices.Equal(k.Others["BOT"], []string{"b1"}) ||
		!slices.Equal(k.Others["TEST"], []string{"t1"}) || !slices.Equal(k.Others["SYSTEM"], []string{"s1"}) {
		t.Fatalf("activity of %+v", k)
	}
	h.svc.KindIDs = failingKindIDs{}
	if d, err = h.svc.Dashboard(ctx, auditor, 7); err != nil || !slices.Equal(d.Partial, []string{"kinds"}) || records.activity[1].Keep.On() {
		t.Fatalf("without the kinds: %+v %+v %v", d.Partial, records.activity[1], err)
	}
}

// failingKindIDs cannot read the accounts' kinds.
type failingKindIDs struct{}

func (failingKindIDs) IDs(context.Context, []string) ([]string, error) {
	return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "user-service is down")
}

// The read models' reports and lists of steps (L1): the humans' by
// default, another kind's when asked; a liquidation list's step kind is
// kind, its accounts' user_kind (transport).
func TestReportsByKind(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	reports := &kindReports{}
	h.svc.Reports, h.svc.MarginReports = reports, reports
	h.svc.KindIDs = &fakeKindIDs{of: map[string][]string{"BOT": {"b1"}, "TEST": {"t1"}, "SYSTEM": {"s1"}}}
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	if _, err := h.svc.TradingReport(ctx, auditor, ReportQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.WalletReport(ctx, auditor, ReportQuery{Kinds: []string{"BOT"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.UsersReport(ctx, auditor, ReportQuery{Kinds: []string{"ALL"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.OpenInterest(ctx, auditor, []string{"SYSTEM"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.Liquidations(ctx, auditor, ports.LiquidationQuery{Kind: "ADL"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.MarginLiquidations(ctx, auditor, MarginLiquidationQuery{Kinds: []string{"TEST"}, UserID: someUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.MarginInterest(ctx, auditor, ReportQuery{Kinds: []string{"TEST"}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.DerivativesReport(ctx, auditor, ReportQuery{Kinds: []string{"robot"}}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown kind: %v", err)
	}
	want := []string{
		"trading except [b1 t1 s1]", "wallet only [b1]", "users every kind", "open interest only [s1]", "liquidations except [b1 t1 s1]",
		"margin liquidations every kind", "margin interest only [t1]",
	}
	if !slices.Equal(reports.asked, want) {
		t.Fatalf("asked\n%q\nwant\n%q", reports.asked, want)
	}
}

// kindReports records the kinds each report or list was asked of.
type kindReports struct {
	ports.Reports
	ports.MarginReports
	asked []string
}

func (r *kindReports) record(what string, f ports.KindFilter) {
	switch {
	case f.Only != nil:
		r.asked = append(r.asked, fmt.Sprintf("%s only %v", what, f.Only))
	case f.Except != nil:
		r.asked = append(r.asked, fmt.Sprintf("%s except %v", what, f.Except))
	default:
		r.asked = append(r.asked, what+" every kind")
	}
}

func (r *kindReports) Trading(_ context.Context, rng ports.ReportRange) ([]ports.TradingDay, error) {
	r.record("trading", rng.ByKind)
	return nil, nil
}

func (r *kindReports) Wallet(_ context.Context, rng ports.ReportRange) ([]ports.WalletDay, error) {
	r.record("wallet", rng.ByKind)
	return nil, nil
}

func (r *kindReports) Derivatives(_ context.Context, rng ports.ReportRange) ([]ports.DerivativesDay, error) {
	r.record("derivatives", rng.ByKind)
	return nil, nil
}

func (r *kindReports) Users(_ context.Context, rng ports.ReportRange) ([]ports.UsersBucket, uint64, error) {
	r.record("users", rng.ByKind)
	return nil, 0, nil
}

func (r *kindReports) OpenInterest(_ context.Context, f ports.KindFilter) ([]ports.OpenInterest, error) {
	r.record("open interest", f)
	return nil, nil
}

func (r *kindReports) Liquidations(_ context.Context, q ports.LiquidationQuery) ([]ports.LiquidationStep, string, error) {
	r.record("liquidations", q.ByKind)
	return nil, "", nil
}

func (r *kindReports) MarginLiquidations(_ context.Context, q ports.MarginLiquidationQuery) ([]ports.MarginLiquidation, string, error) {
	r.record("margin liquidations", q.ByKind)
	return nil, "", nil
}

func (r *kindReports) MarginInterest(_ context.Context, rng ports.ReportRange, _ string) ([]ports.MarginInterestBucket, error) {
	r.record("margin interest", rng.ByKind)
	return nil, nil
}

// The lists services serve (L1 over L2/L3's POST .../list): the humans'
// by default, another kind's when asked, a user's whatever its kind; the
// badges count the humans'; a filter cut to MaxKindIDs is said in the
// answer (kinds_narrowed).
func TestServiceListsByKind(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	m := newFakeMargin()
	h.svc.Margin = m
	h.svc.KindIDs = &fakeKindIDs{of: map[string][]string{"BOT": {"b1"}, "TEST": {"t1"}, "SYSTEM": {"s1"}}}
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	boss := h.login(t, "boss@example.com")
	humans := ports.KindFilter{Except: []string{"b1", "t1", "s1"}}
	bots := ports.KindFilter{Only: []string{"b1"}}
	same := func(what string, got []ports.KindFilter, want ...ports.KindFilter) {
		t.Helper()
		if !slices.EqualFunc(got, want, func(a, b ports.KindFilter) bool {
			return slices.Equal(a.Only, b.Only) && slices.Equal(a.Except, b.Except) && (a.Only == nil) == (b.Only == nil) && a.Narrowed == b.Narrowed
		}) {
			t.Fatalf("%s: %+v, want %+v", what, got, want)
		}
	}

	for _, q := range []ports.WithdrawalQuery{{}, {Kinds: []string{"BOT"}}, {Kinds: []string{"BOT"}, UserID: someUser}} {
		if raw, err := h.svc.Withdrawals(ctx, boss, q); err != nil || strings.Contains(string(raw), "kinds_narrowed") {
			t.Fatalf("withdrawals %+v: %s %v", q, raw, err)
		}
	}
	same("withdrawals", h.wallet.lists, humans, bots, ports.KindFilter{})
	for _, q := range []ports.DepositReviewQuery{{Attention: true}, {Kinds: []string{"ALL"}}} {
		if _, err := h.svc.DepositsForReview(ctx, boss, q); err != nil {
			t.Fatal(err)
		}
	}
	same("deposits to handle", h.deposits.lists, humans, ports.KindFilter{})
	if _, err := h.svc.CustodyFees(ctx, boss, ports.FeeQuery{Kinds: []string{"bot"}}); err != nil {
		t.Fatal(err)
	}
	same("custody fees", h.wallet.lists[3:], bots)
	if _, err := h.svc.OpenPositions(ctx, boss, ports.PositionQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.DerivativesRisk(ctx, boss, []string{"BOT"}); err != nil {
		t.Fatal(err)
	}
	same("positions", []ports.KindFilter{h.derivatives.queries[0].ByKind}, humans)
	same("contract risk", h.derivatives.risk, bots)
	if _, err := h.svc.MarginAccounts(ctx, boss, MarginAccountQuery{}); err != nil {
		t.Fatal(err)
	}
	same("margin accounts", m.lists, humans)
	if _, err := h.svc.Withdrawals(ctx, boss, ports.WithdrawalQuery{Kinds: []string{"ROBOT"}}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown kind: %v", err)
	}

	// The badges count the humans' withdrawals and deposits.
	h.wallet.lists, h.deposits.lists = nil, nil
	if _, err := h.svc.Todo(ctx, boss); err != nil {
		t.Fatal(err)
	}
	same("the withdrawals badge", h.wallet.lists, humans)
	same("the deposits badge", h.deposits.lists, humans)

	// Past MaxKindIDs test accounts: the humans' leave out the bots and
	// HOUSE only, and the answers say so.
	many := make([]string, MaxKindIDs+1)
	for i := range many {
		many[i] = fmt.Sprintf("t%d", i)
	}
	h.svc.KindIDs = &fakeKindIDs{of: map[string][]string{"BOT": {"b1"}, "TEST": many, "SYSTEM": {"s1"}}}
	narrowed := ports.KindFilter{Except: []string{"b1", "s1"}, Narrowed: true}
	h.wallet.lists = nil
	for name, list := range map[string]func() (json.RawMessage, error){
		"withdrawals":        func() (json.RawMessage, error) { return h.svc.Withdrawals(ctx, boss, ports.WithdrawalQuery{}) },
		"deposits to handle": func() (json.RawMessage, error) { return h.svc.DepositsForReview(ctx, boss, ports.DepositReviewQuery{}) },
		"custody fees":       func() (json.RawMessage, error) { return h.svc.CustodyFees(ctx, boss, ports.FeeQuery{}) },
		"contract risk":      func() (json.RawMessage, error) { return h.svc.DerivativesRisk(ctx, boss, nil) },
		"margin accounts":    func() (json.RawMessage, error) { return h.svc.MarginAccounts(ctx, boss, MarginAccountQuery{}) },
		"positions": func() (json.RawMessage, error) {
			page, err := h.svc.OpenPositions(ctx, boss, ports.PositionQuery{})
			if err != nil {
				return nil, err
			}
			return json.Marshal(page)
		},
	} {
		raw, err := list()
		var page struct {
			Narrowed bool `json:"kinds_narrowed"`
		}
		if err != nil || json.Unmarshal(raw, &page) != nil || !page.Narrowed {
			t.Fatalf("%s: %s %v", name, raw, err)
		}
	}
	same("the withdrawals and fees narrowed", h.wallet.lists, narrowed, narrowed)
}
