package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
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

func TestAMintHasACapAndIsFinishedWhenPartlyBooked(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sim := &stateSim{}
	h.svc.Sim, h.svc.SimBots = sim, sim
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	boss, fin := h.login(t, "boss@example.com"), h.login(t, "fin@example.com")
	mint := func(p Principal, asset, amount string) (domain.Approval, error) {
		return h.svc.MintSimBots(ctx, p, SimMintInput{Asset: asset, Amount: decimal.RequireFromString(amount), Reason: "the bots run low"})
	}

	// Whoever would approve it: at most 10,000,000 of the coin, 1,000,000 USDT.
	h.svc.Features = onFlags{flags.KeyTwoPerson: true}
	for asset, amount := range map[string]string{"ASTRA": "10000000.01", "USDT": "1000000.01"} {
		if _, err := mint(fin, asset, amount); !apperr.Is(err, "ADMIN_SIM_MINT_CAP") {
			t.Fatalf("%s %s beyond the cap: %v", amount, asset, err)
		}
	}
	if a, err := mint(fin, "ASTRA", "10000000"); err != nil || a.Status != domain.ApprovalPending {
		t.Fatalf("the cap itself %+v %v", a, err)
	}
	h.svc.Features = nil

	// The second bot refuses: the first is booked, the mint stays pending
	// and its requester finishes it once the cause is fixed; the keys
	// book only the rest.
	h.ledger.calls = nil
	h.ledger.err, h.ledger.refuse = apperr.New(apperr.KindForbidden, "LEDGER_ACCOUNT_FROZEN", "frozen"), botB
	_, err := mint(boss, "USDT", "30")
	e := apperr.From(err)
	if e == nil || e.Code != "LEDGER_ACCOUNT_FROZEN" || e.Details["booked"] != 1 || e.Details["of"] != 3 || e.Details["bot"] != "bot-02" {
		t.Fatalf("partly booked: %v", err)
	}
	id, _ := e.Details["approval_id"].(string)
	a, err := h.store.Read().Approvals().Get(ctx, id)
	if err != nil || a == nil || a.Status != domain.ApprovalPending || a.Mode != domain.ModeSingle {
		t.Fatalf("kept pending %+v %v", a, err)
	}
	// It says how far it got (C5.5 ⑭), and nobody rejects it: a new mint would book bot-01 twice.
	if a.AttemptedAt.IsZero() || a.Result != "booked 1 of 3; bot-02: LEDGER_ACCOUNT_FROZEN: frozen" {
		t.Fatalf("its progress %+v", a)
	}
	if _, err := h.svc.DecideApproval(ctx, boss, id, false, "start over"); code(err) != "ADMIN_APPROVAL_ATTEMPTED" {
		t.Fatalf("withdrawn after booking a part: %v", err)
	}
	if used, err := h.store.Read().Approvals().SingleUsage(ctx, boss.Admin.ID, h.now.Add(-time.Hour)); err != nil || !used.Equal(decimal.NewFromInt(30)) {
		t.Fatalf("counted in the day's single-person total: %s %v", used, err)
	}
	h.ledger.err, h.ledger.refuse = nil, ""
	done, err := h.svc.DecideApproval(ctx, boss, id, true, "unfrozen, finishing it")
	if err != nil || done.Status != domain.ApprovalExecuted || !strings.HasPrefix(done.Result, "3 adjustments") {
		t.Fatalf("finished %+v %v", done, err)
	}
	keys := map[string]int{}
	for _, c := range h.ledger.calls {
		keys[c.key]++
	}
	if len(keys) != 3 || keys["approval:"+id+":"+botA] != 2 || keys["approval:"+id+":"+botB] != 2 || keys["approval:"+id+":"+botC] != 1 {
		t.Fatalf("each bot under its own key %v", keys)
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

// coinLedger answers the coin's holders and system accounts as the ledger
// holds them now (A123).
type coinLedger struct{ *fakeLedger }

func (coinLedger) SystemBalances(_ context.Context, asset string) ([]ports.Balance, error) {
	if asset != "ASTRA" {
		return nil, fmt.Errorf("asked %s", asset)
	}
	return []ports.Balance{
		{AccountType: "ADJUSTMENT", Asset: asset, Available: "-1000", Frozen: "0"},
		{AccountType: "FEE_REVENUE", Asset: asset, Available: "0.1", Frozen: "0"},
		{AccountType: "INSURANCE_FUND", Asset: asset, Available: "0", Frozen: "0"},
	}, nil
}

// Who holds the coin (A123, A123b, A125): the ledger's balances now - the
// bots (market-sim's) apart from the users with what each kind holds, a
// debt included, and how many hold some; HOUSE (a system user) in the
// platform's row; the test accounts apart, one owing among them; the
// system accounts not at zero; the largest holders above zero, neither
// HOUSE nor a test account among them.
func TestWhoHoldsTheCoin(t *testing.T) {
	const house, test, testOwing = "0192a000-0000-7000-8000-0000000000aa", "0192a000-0000-7000-8000-0000000000e2", "0192a000-0000-7000-8000-0000000000e3"
	user1, user2, owing := "0192a000-0000-7000-8000-0000000000c1", "0192a000-0000-7000-8000-0000000000c2", "0192a000-0000-7000-8000-0000000000c3"
	led := &fakeLedger{holders: map[string][]ports.Holder{"ASTRA": {
		{UserID: botA, Amount: decimal.NewFromInt(600)},
		{UserID: house, Amount: decimal.NewFromInt(500)},
		{UserID: botB, Amount: decimal.NewFromInt(390)},
		{UserID: test, Amount: decimal.NewFromInt(52)},
		{UserID: user1, Amount: decimal.RequireFromString("9.9")},
		{UserID: user2, Amount: decimal.RequireFromString("9.9")},
		{UserID: owing, Amount: decimal.RequireFromString("-0.5")},
		{UserID: testOwing, Amount: decimal.NewFromInt(-2)},
	}}}
	kinds := &fakeKindIDs{of: map[string][]string{"SYSTEM": {house}, "TEST": {test, testOwing}}}
	at := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	svc := &Service{Sim: &stateSim{}, Ledger: coinLedger{led}, KindIDs: kinds, Now: func() time.Time { return at }}
	tok, err := svc.SimTokenHoldings(context.Background(), reader)
	if err != nil || tok.Asset != "ASTRA" || tok.Price == nil || tok.Price.String() != "1.25" || !tok.At.Equal(at) {
		t.Fatalf("the coin %+v %v", tok, err)
	}
	h := tok.Holdings
	if h.Bots.String() != "990" || h.BotHolders != 2 || h.Users.String() != "19.3" || h.UserHolders != 2 ||
		h.Test == nil || h.Test.Amount.String() != "50" || h.Test.Holders != 1 || len(h.Partial) != 0 ||
		len(h.System) != 3 || h.System["ADJUSTMENT"].String() != "-1000" || h.System["FEE_REVENUE"].String() != "0.1" || h.System["HOUSE"].String() != "500" {
		t.Fatalf("holdings %+v", h)
	}
	if len(h.Top) != 4 || h.Top[0].UserID != botA || h.Top[1].UserID != botB || h.Top[2].UserID != user1 || h.Top[3].UserID != user2 {
		t.Fatalf("the largest holders but HOUSE and the test account, the user IDs settling a tie %+v", h.Top)
	}
	// Without the accounts' kinds and without HOUSE_USER_ID configured: HOUSE
	// and the test accounts among the users, told so.
	svc.KindIDs = nil
	if tok, err := svc.SimTokenHoldings(context.Background(), reader); err != nil || tok.Users.String() != "569.3" || tok.UserHolders != 4 ||
		tok.Test != nil || !slices.Equal(tok.Partial, []string{"kinds"}) || len(tok.Top) != 6 || tok.Top[1].UserID != house {
		t.Fatalf("the kinds unknown %+v %v", tok.Holdings, err)
	}
	// HOUSE's account from the configuration (A128): user-service down, the
	// test accounts among the users, HOUSE not; HOUSE not marked SYSTEM, the
	// same without a note.
	svc.HouseBook.User, svc.Log = house, slog.New(slog.DiscardHandler)
	svc.KindIDs = &fakeKindIDs{err: errors.New("user-service down")}
	if tok, err := svc.SimTokenHoldings(context.Background(), reader); err != nil || tok.Users.String() != "69.3" || tok.UserHolders != 3 ||
		tok.System["HOUSE"].String() != "500" || tok.Test != nil || !slices.Equal(tok.Partial, []string{"kinds"}) ||
		len(tok.Top) != 5 || tok.Top[2].UserID != test {
		t.Fatalf("the kinds unknown, HOUSE known %+v %v", tok.Holdings, err)
	}
	svc.KindIDs = &fakeKindIDs{of: map[string][]string{"TEST": {test, testOwing}}}
	if tok, err := svc.SimTokenHoldings(context.Background(), reader); err != nil || tok.Users.String() != "19.3" ||
		tok.System["HOUSE"].String() != "500" || tok.Test == nil || len(tok.Partial) != 0 || len(tok.Top) != 4 {
		t.Fatalf("HOUSE not marked SYSTEM %+v %v", tok.Holdings, err)
	}
	// The list cut at its limit: the test accounts stay among the users, told so.
	m, err := svc.coinBalances(context.Background(), "ASTRA", []string{botA, botB}, []string{house})
	if err != nil || m.limit != 1000 || len(m.all.Top) != 8 {
		t.Fatalf("read %+v %v", m, err)
	}
	m.all.Top, m.limit = m.all.Top[:2], 2
	cut, err := holdingsOf(m, holderKinds{system: []string{house}, test: map[string]bool{test: true, testOwing: true}}, 20)
	if err != nil || cut.Test != nil || !slices.Equal(cut.Partial, []string{"test"}) || cut.Users.String() != "69.3" || cut.UserHolders != 3 {
		t.Fatalf("too many to tell %+v %v", cut, err)
	}
}

// countingLedger counts the reads of the coin's holders and the users
// asked apart.
type countingLedger struct {
	coinLedger
	reads []int
}

func (l *countingLedger) Holders(ctx context.Context, asset string, limit int, apart []string) (ports.HolderPage, error) {
	l.reads = append(l.reads, limit)
	return l.coinLedger.Holders(ctx, asset, limit, apart)
}

// More holders than are listed (A128): HOUSE's sum is read on its own, the
// test accounts stay among the users; every holder listed, HOUSE's comes
// from the same read as the sums.
func TestTheCoinWithMoreHoldersThanListed(t *testing.T) {
	const house, test = "0192a000-0000-7000-8000-0000000000aa", "0192a000-0000-7000-8000-0000000000e2"
	holders := []ports.Holder{
		{UserID: botA, Amount: decimal.NewFromInt(600)},
		{UserID: house, Amount: decimal.NewFromInt(500)},
		{UserID: test, Amount: decimal.NewFromInt(50)},
	}
	for i := range simHolderLimit {
		holders = append(holders, ports.Holder{UserID: fmt.Sprintf("0192a000-0000-7000-8000-%012d", i), Amount: decimal.NewFromInt(1)})
	}
	led := &countingLedger{coinLedger: coinLedger{&fakeLedger{holders: map[string][]ports.Holder{"ASTRA": holders}}}}
	kinds := &fakeKindIDs{of: map[string][]string{"SYSTEM": {house}, "TEST": {test}}}
	svc := &Service{Sim: &stateSim{}, Ledger: led, KindIDs: kinds, Now: time.Now}
	tok, err := svc.SimTokenHoldings(context.Background(), reader)
	if err != nil || !slices.Equal(led.reads, []int{simHolderLimit, simHolderLimit, 1}) {
		t.Fatalf("read %v: %v", led.reads, err)
	}
	if tok.Bots.String() != "600" || tok.BotHolders != 1 || tok.System["HOUSE"].String() != "500" || tok.Test != nil ||
		!slices.Equal(tok.Partial, []string{"test"}) || tok.Users.String() != "1050" || tok.UserHolders != 1001 ||
		len(tok.Top) != 20 || tok.Top[0].UserID != botA || tok.Top[1].UserID != "0192a000-0000-7000-8000-000000000000" {
		t.Fatalf("more than listed, the test account known all the same %+v", tok.Holdings)
	}
	// Every holder listed: no read of its own.
	led.holders["ASTRA"], led.reads = holders[:3], nil
	if tok, err := svc.SimTokenHoldings(context.Background(), reader); err != nil || !slices.Equal(led.reads, []int{simHolderLimit, simHolderLimit}) ||
		tok.System["HOUSE"].String() != "500" || tok.Test == nil || tok.Test.Amount.String() != "50" {
		t.Fatalf("all listed, read %v: %+v %v", led.reads, tok.Holdings, err)
	}
}

// movingLedger is a market trading while the page reads it: every read is
// a moment later, and until moment still each moment's trade pays a fee of
// 0.1 from the holder to FEE_REVENUE.
type movingLedger struct {
	*fakeLedger
	now, still, reads int
}

func (l *movingLedger) fees() decimal.Decimal {
	l.now++
	return decimal.NewFromInt(int64(min(l.now, l.still))).Div(decimal.NewFromInt(10))
}

func (l *movingLedger) Holders(_ context.Context, _ string, _ int, apart []string) (ports.HolderPage, error) {
	l.reads++
	held := ports.HolderSum{Amount: decimal.NewFromInt(100).Sub(l.fees()), Holders: 1}
	if slices.Contains(apart, botA) {
		return ports.HolderPage{Top: []ports.Holder{{UserID: botA, Amount: held.Amount}}, Apart: held}, nil
	}
	return ports.HolderPage{Top: []ports.Holder{{UserID: botA, Amount: held.Amount}}, Others: held}, nil
}

func (l *movingLedger) SystemBalances(context.Context, string) ([]ports.Balance, error) {
	return []ports.Balance{{AccountType: "ADJUSTMENT", Available: "-100", Frozen: "0"}, {AccountType: "FEE_REVENUE", Available: l.fees().String(), Frozen: "0"}}, nil
}

// The holders and the system accounts are read as one moment (A123): the
// holders again until their total holds still around the system accounts'
// read, so that what they hold adds up to what was issued.
func TestTheCoinReadAsOneMoment(t *testing.T) {
	led := &movingLedger{fakeLedger: &fakeLedger{}, still: 3}
	svc := &Service{Sim: &stateSim{}, Ledger: led, Now: time.Now, Log: slog.New(slog.DiscardHandler)}
	tok, err := svc.SimTokenHoldings(context.Background(), reader)
	if err != nil || led.reads != 3 {
		t.Fatalf("the holders read %d times: %v", led.reads, err)
	}
	held := tok.Bots.Add(tok.Users).Add(tok.System["FEE_REVENUE"])
	if !held.Equal(decimal.NewFromInt(100)) || !tok.System["ADJUSTMENT"].Equal(decimal.NewFromInt(-100)) {
		t.Fatalf("held %s of 100 issued: %+v", held, tok.Holdings)
	}
	// Never still: three tries, then the last read stands.
	led = &movingLedger{fakeLedger: &fakeLedger{}, still: 99}
	svc.Ledger = led
	if _, err := svc.SimTokenHoldings(context.Background(), reader); err != nil || led.reads != 4 {
		t.Fatalf("the holders read %d times: %v", led.reads, err)
	}
}

// downLedger is ledger-service not answering who holds the coin.
type downLedger struct{ *fakeLedger }

func (downLedger) Holders(context.Context, string, int, []string) (ports.HolderPage, error) {
	return ports.HolderPage{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "ledger-service is down")
}

// Nobody holds the coin yet: no holder, an empty list of the largest, the
// system accounts as they are; ledger-service down: unavailable, not zero
// (A125 ③).
func TestTheCoinWithoutHoldersOrLedger(t *testing.T) {
	svc := &Service{Sim: &stateSim{}, Ledger: &fakeLedger{}, KindIDs: &fakeKindIDs{}, Now: time.Now}
	tok, err := svc.SimTokenHoldings(context.Background(), reader)
	if err != nil || tok.UserHolders != 0 || tok.BotHolders != 0 || tok.Top == nil || len(tok.Top) != 0 || !tok.Users.IsZero() ||
		tok.Test == nil || !tok.Test.Amount.IsZero() || tok.System["FEE_REVENUE"].String() != "7" || tok.System["PNL_CLEARING"].String() != "-12.5" {
		t.Fatalf("no holders %+v %v", tok.Holdings, err)
	}
	svc.Ledger = downLedger{&fakeLedger{}}
	if _, err := svc.SimTokenHoldings(context.Background(), reader); apperr.From(err).Kind != apperr.KindUnavailable {
		t.Fatalf("ledger-service down: %v", err)
	}
}

// pageRecords answers one page of orders and trades and keeps the query.
type pageRecords struct {
	ports.Records
	orders ports.OrderQuery
	trades ports.TradeQuery
	// activity is the accounts the overview's figures were asked of.
	activity []ports.ActivityKinds
}

func (f *pageRecords) Activity(_ context.Context, _ int, k ports.ActivityKinds) (ports.Activity, error) {
	f.activity = append(f.activity, k)
	return ports.Activity{}, nil
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
