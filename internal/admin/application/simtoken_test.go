package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

const (
	botA = "0192a000-0000-7000-8000-00000000000a"
	botB = "0192a000-0000-7000-8000-00000000000b"
	botC = "0192a000-0000-7000-8000-00000000000c"
)

// stateSim is market-sim with two makers and a taker, and a last price.
type stateSim struct{ fakeSim }

func (s *stateSim) Status(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"symbol":"ASTRA-USDT","last_price":"1.25","bots":[
		{"user_id":"` + botA + `","role":"MAKER","label":"bot-01"},
		{"user_id":"` + botB + `","role":"MAKER","label":"bot-02"},
		{"user_id":"` + botC + `","role":"TAKER","label":"bot-03"}]}`), nil
}

func TestMoreForTheBotsIsAFundOperation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sim := &stateSim{}
	h.svc.Sim, h.svc.SimBots = sim, sim
	for email, role := range map[string]string{
		"ops@example.com": domain.RoleOperator, "boss@example.com": domain.RoleAdmin, "fin@example.com": domain.RoleFinance,
	} {
		h.admin(t, email, role)
	}
	ops, boss, fin := h.login(t, "ops@example.com"), h.login(t, "boss@example.com"), h.login(t, "fin@example.com")
	mint := func(p Principal, asset, amount, role string) (domain.Approval, error) {
		return h.svc.MintSimBots(ctx, p, SimMintInput{Asset: asset, Amount: decimal.RequireFromString(amount), Role: role, Reason: "the bots run low"})
	}

	if _, err := mint(ops, "USDT", "100", ""); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR prints no money: %v", err)
	}
	if _, err := mint(boss, "BTC", "1", ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("the bots hold the coin and USDT: %v", err)
	}
	if _, err := mint(boss, "USDT", "0.02", ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("too little to share: %v", err)
	}
	if _, err := mint(boss, "USDT", "100", "EXECUTOR"); code(err) != "ADMIN_SIM_NO_BOTS" {
		t.Fatalf("no executors: %v", err)
	}
	if len(h.ledger.calls) != 0 {
		t.Fatalf("nothing booked yet: %+v", h.ledger.calls)
	}

	// Single-person mode within the limits: booked at once, one adjustment
	// per bot under its own key, the first taking the rounding.
	a, err := mint(boss, "usdt", "100", "")
	if err != nil || a.Status != domain.ApprovalExecuted || a.Mode != domain.ModeSingle || a.Kind != domain.KindSimMint ||
		a.JournalID != "journal-1" || a.Result != "3 adjustments, journals journal-1 to journal-1" {
		t.Fatalf("at once %+v %v", a, err)
	}
	var got []string
	for _, c := range h.ledger.calls {
		got = append(got, c.key+" "+c.userID+" "+c.account+" "+c.asset+" "+c.amount.String()+" "+c.actor+" "+c.memo)
	}
	want := []string{
		"approval:" + a.ID + ":" + botA + " " + botA + " SPOT USDT 33.34 boss@example.com the bots run low",
		"approval:" + a.ID + ":" + botB + " " + botB + " SPOT USDT 33.33 boss@example.com the bots run low",
		"approval:" + a.ID + ":" + botC + " " + botC + " SPOT USDT 33.33 boss@example.com the bots run low",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("booked\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(a.Payload["bots"], `{"user_id":"`+botA+`","label":"bot-01","amount":"33.34"}`) || a.Payload["amount"] != "100" {
		t.Fatalf("the shares are kept with it: %+v", a.Payload)
	}
	last := h.store.audits[len(h.store.audits)-1]
	if last.GetAction() != "admin.sim.mint_executed" || last.GetTarget() != "sim" || !strings.Contains(last.GetDetails(), `"bots":[{"user_id"`) {
		t.Fatalf("audited %v", last)
	}

	// Two-person mode: the makers' share waits for a second administrator
	// who may approve fund operations.
	h.svc.Features = onFlags{flags.KeyTwoPerson: true}
	a, err = mint(fin, "ASTRA", "1000", "maker")
	if err != nil || a.Status != domain.ApprovalPending || a.Escalation != domain.EscalationTwoPerson || a.Payload["role"] != "MAKER" {
		t.Fatalf("waits %+v %v", a, err)
	}
	if _, err := h.svc.DecideApproval(ctx, fin, a.ID, true, "my own"); !apperr.Is(err, "ADMIN_SELF_APPROVAL") {
		t.Fatalf("nobody approves their own: %v", err)
	}
	if _, err := h.svc.DecideApproval(ctx, ops, a.ID, true, "ops approves"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR approves no fund operation: %v", err)
	}
	if a, err = h.svc.DecideApproval(ctx, boss, a.ID, true, "checked the inventory"); err != nil || a.Status != domain.ApprovalExecuted {
		t.Fatalf("approved %+v %v", a, err)
	}
	if n := len(h.ledger.calls); n != 5 || h.ledger.calls[3].userID != botA || h.ledger.calls[4].userID != botB ||
		h.ledger.calls[4].amount.String() != "500" || h.ledger.calls[4].asset != "ASTRA" || h.ledger.calls[4].actor != "boss@example.com" {
		t.Fatalf("the makers' share %+v", h.ledger.calls)
	}

	// A refusal fails it; no answer leaves it pending, its keys making a
	// second attempt safe.
	h.svc.Features = nil
	h.ledger.err = apperr.New(apperr.KindForbidden, "LEDGER_ADJUSTMENTS_OFF", "manual adjustments are off")
	if a, err = mint(boss, "USDT", "30", ""); err != nil || a.Status != domain.ApprovalFailed ||
		a.Result != "bot-01: LEDGER_ADJUSTMENTS_OFF: manual adjustments are off" {
		t.Fatalf("refused %+v %v", a, err)
	}
	h.ledger.err = apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "ledger down")
	if _, err = mint(boss, "USDT", "30", ""); code(err) != apperr.CodeUnavailable {
		t.Fatalf("no answer %v", err)
	}
}

func TestSplittingAMint(t *testing.T) {
	bots := []simBot{{UserID: botA, Label: "a"}, {UserID: botB, Label: "b"}, {UserID: botC, Label: "c"}, {UserID: botA, Label: "d"}}
	shares, err := splitMint(decimal.RequireFromString("1000000.07"), bots)
	if err != nil {
		t.Fatal(err)
	}
	sum := decimal.Zero
	var amounts []string
	for _, s := range shares {
		sum = sum.Add(decimal.RequireFromString(s.Amount))
		amounts = append(amounts, s.Amount)
	}
	if !sum.Equal(decimal.RequireFromString("1000000.07")) || !slices.Equal(amounts, []string{"250000.04", "250000.01", "250000.01", "250000.01"}) {
		t.Fatalf("shares %v sum %s", amounts, sum)
	}
	if _, err := splitMint(decimal.NewFromInt(1), nil); !errors.Is(err, ErrNoBots) {
		t.Fatalf("no bots: %v", err)
	}
}

// holdingsReports records what the coin's page asked.
type holdingsReports struct {
	ports.Reports
	asset string
	bots  []string
	top   int
}

func (f *holdingsReports) Holdings(_ context.Context, asset string, bots []string, top int) (ports.Holdings, error) {
	f.asset, f.bots, f.top = asset, bots, top
	return ports.Holdings{Bots: decimal.NewFromInt(999), BotHolders: 3, System: map[string]decimal.Decimal{"ADJUSTMENT": decimal.NewFromInt(-1000)}}, nil
}

func TestWhoHoldsTheCoin(t *testing.T) {
	reports := &holdingsReports{}
	at := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	svc := &Service{Sim: &stateSim{}, Reports: reports, Now: func() time.Time { return at }}
	tok, err := svc.SimTokenHoldings(context.Background(), reader)
	if err != nil || tok.Asset != "ASTRA" || tok.Price == nil || tok.Price.String() != "1.25" || !tok.At.Equal(at) || tok.BotHolders != 3 {
		t.Fatalf("the coin %+v %v", tok, err)
	}
	if reports.asset != "ASTRA" || !slices.Equal(reports.bots, []string{botA, botB, botC}) || reports.top != simTopHolders {
		t.Fatalf("asked %+v", reports)
	}
}

// pageRecords answers one page of orders and trades and keeps the query.
type pageRecords struct {
	ports.Records
	orders ports.OrderQuery
	trades ports.TradeQuery
}

func (f *pageRecords) Orders(_ context.Context, q ports.OrderQuery) ([]ports.Order, string, error) {
	f.orders = q
	return []ports.Order{{OrderID: "o1", UserID: "bot-1"}, {OrderID: "o2", UserID: "user-1"}}, "", nil
}

func (f *pageRecords) Trades(_ context.Context, q ports.TradeQuery) ([]ports.Trade, string, error) {
	f.trades = q
	return []ports.Trade{{TradeID: "t1", BuyerUserID: "bot-1", SellerUserID: "user-1"}}, "", nil
}

func TestListsMarkAndKeepTheBots(t *testing.T) {
	ctx := context.Background()
	records := &pageRecords{}
	svc := &Service{Records: records, SimBots: fakeBots{ids: []string{"bot-1"}}, Log: slog.New(slog.DiscardHandler)}
	orders, _, err := svc.OrderList(ctx, reader, ports.OrderQuery{})
	if err != nil || !orders[0].Bot || orders[1].Bot || !slices.Equal(records.orders.Bots, []string{"bot-1"}) {
		t.Fatalf("marked %+v %+v %v", orders, records.orders, err)
	}
	trades, _, err := svc.TradeList(ctx, reader, ports.TradeQuery{Accounts: ports.AccountsBots})
	if err != nil || !trades[0].BuyerBot || trades[0].SellerBot || records.trades.Accounts != "bots" {
		t.Fatalf("trades %+v %v", trades, err)
	}
	if _, _, err := svc.OrderList(ctx, reader, ports.OrderQuery{Accounts: "robots"}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("bots or users: %v", err)
	}

	// Without market-sim a list still shows, unmarked; keeping or leaving
	// out the bots needs it.
	svc.SimBots = fakeBots{err: errors.New("market-sim down")}
	if orders, _, err = svc.OrderList(ctx, reader, ports.OrderQuery{}); err != nil || orders[0].Bot {
		t.Fatalf("unmarked %+v %v", orders, err)
	}
	if _, _, err := svc.TradeList(ctx, reader, ports.TradeQuery{Accounts: ports.AccountsUsers}); err == nil {
		t.Fatal("the users' trades need the bots")
	}
	svc.SimBots = nil
	if _, _, err := svc.OrderList(ctx, reader, ports.OrderQuery{Accounts: ports.AccountsBots}); code(err) != apperr.CodeUnavailable {
		t.Fatalf("no market-sim here: %v", err)
	}
}
