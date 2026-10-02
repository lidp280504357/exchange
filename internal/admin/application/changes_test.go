package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/flags"
)

// docCatalog keeps the reference data as items of a config document and
// works out a document's changes as instrument-service does: whole items,
// created or updated with a new version.
type docCatalog struct {
	ports.Instruments
	items map[string]map[string]map[string]any // entity → key → item
	order []string
	real  int
}

var entityKeys = map[string]struct{ section, key string }{
	"FEE_SCHEDULE": {"fee_schedules", "tier"}, "TRADING_PAIR": {"pairs", "symbol"}, "CONTRACT": {"contracts", "symbol"},
}

func newDocCatalog(t *testing.T, doc string) *docCatalog {
	t.Helper()
	c := &docCatalog{items: map[string]map[string]map[string]any{}}
	if _, err := c.Apply(context.Background(), json.RawMessage(doc), false, "seed", "seed"); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *docCatalog) Export(context.Context) (json.RawMessage, error) {
	doc := map[string][]map[string]any{"fee_schedules": {}, "assets": {}, "pairs": {}, "contracts": {}}
	for _, entity := range []string{"FEE_SCHEDULE", "TRADING_PAIR", "CONTRACT"} {
		for _, key := range c.order {
			if it, ok := c.items[entity][key]; ok {
				doc[entityKeys[entity].section] = append(doc[entityKeys[entity].section], it)
			}
		}
	}
	return json.Marshal(doc)
}

func (c *docCatalog) Apply(_ context.Context, config json.RawMessage, dryRun bool, _, _ string) (ports.ConfigResult, error) {
	var doc map[string][]map[string]any
	if err := json.Unmarshal(config, &doc); err != nil {
		return ports.ConfigResult{}, apperr.Invalid("bad document")
	}
	res := ports.ConfigResult{Changes: []ports.ConfigChange{}}
	for _, entity := range []string{"FEE_SCHEDULE", "TRADING_PAIR", "CONTRACT"} {
		ek := entityKeys[entity]
		for _, it := range doc[ek.section] {
			key, _ := it[ek.key].(string)
			cur, known := c.items[entity][key]
			next := map[string]any{}
			for k, v := range it {
				if k != "version" && k != "status" {
					next[k] = v
				}
			}
			version := 1.0
			if known {
				next["status"], version = cur["status"], cur["version"].(float64)+1
				if same(cur, next) {
					res.Unchanged++
					continue
				}
			} else if next["status"] = it["status"]; next["status"] == nil {
				next["status"] = "PREPARE"
			}
			next["version"] = version
			before, after := json.RawMessage("null"), mustJSON(next)
			action := "CREATE"
			if known {
				before, action = mustJSON(cur), "UPDATE"
			}
			res.Changes = append(res.Changes, ports.ConfigChange{Entity: entity, Key: key, Action: action, Version: int64(version), Before: before, After: after})
			if !dryRun {
				if c.items[entity] == nil {
					c.items[entity] = map[string]map[string]any{}
				}
				if !known && !slices.Contains(c.order, key) {
					c.order = append(c.order, key)
				}
				c.items[entity][key] = next
			}
		}
	}
	if !dryRun {
		c.real++
	}
	return res, nil
}

func (c *docCatalog) setStatus(entity, symbol, to string) (string, error) {
	it, ok := c.items[entity][symbol]
	if !ok {
		return "", apperr.NotFound("no such item")
	}
	from := it["status"].(string)
	it["status"], it["version"] = to, it["version"].(float64)+1
	return from, nil
}

func (c *docCatalog) SetPairStatus(_ context.Context, symbol, to, _, _ string) (string, error) {
	return c.setStatus("TRADING_PAIR", symbol, to)
}

func (c *docCatalog) SetContractStatus(_ context.Context, symbol, to, _, _ string) (string, error) {
	return c.setStatus("CONTRACT", symbol, to)
}

func same(a, b map[string]any) bool {
	strip := func(m map[string]any) string {
		c := map[string]any{}
		for k, v := range m {
			if k != "version" {
				c[k] = v
			}
		}
		return string(mustJSON(c))
	}
	return strip(a) == strip(b)
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

const seedDoc = `{
	"fee_schedules": [{"tier":"default","maker_fee_rate":"0.001","taker_fee_rate":"0.001"}],
	"pairs": [
		{"symbol":"BTC-USDT","fee_tier":"default","reference_symbol":"BTCUSDT","reference_multiplier":"1","status":"TRADING"},
		{"symbol":"LINK-USDT","fee_tier":"default","reference_symbol":"LINKUSDT","reference_multiplier":"1","status":"TRADING"},
		{"symbol":"ETH-BTC","fee_tier":"default","reference_symbol":"ETHBTC","reference_multiplier":"1","status":"TRADING"}],
	"contracts": [{"symbol":"BTC-USDT-PERP","index_symbol":"BTC-USDT","fee_tier":"default","status":"TRADING",
		"risk_tiers":[{"max_notional":"50000","max_leverage":125,"mmr":"0.004"}]}]}`

func changeRig(t *testing.T) (*harness, *docCatalog, Principal, Principal, Principal) {
	t.Helper()
	h := newHarness(t)
	catalog := newDocCatalog(t, seedDoc)
	h.svc.Catalog, h.svc.Reference, h.svc.Flags, h.svc.Features = catalog, &fakeReference{}, &houseFlags{}, onFlags{}
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "deputy@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	return h, catalog, h.login(t, "boss@example.com"), h.login(t, "deputy@example.com"), h.login(t, "ops@example.com")
}

func TestTradingParametersWaitForTheirTime(t *testing.T) {
	h, catalog, boss, deputy, ops := changeRig(t)
	ctx := context.Background()
	fee := json.RawMessage(`{"fee_schedules":[{"tier":"default","maker_fee_rate":"0.1","taker_fee_rate":"0.001"}]}`)

	// An OPERATOR sees what the document moves but cannot confirm it.
	prev, err := h.svc.PreviewConfig(ctx, ops, fee)
	if err != nil || len(prev.Guard.Params) != 1 || prev.Guard.Params[0].Field != "maker_fee_rate" || string(prev.Guard.Params[0].After) != `"0.1"` ||
		prev.Guard.Confirmation != nil || prev.Guard.DelaySeconds != 300 {
		t.Fatalf("operator's preview %+v %v", prev.Guard, err)
	}
	if _, _, err := h.svc.ApplyConfig(ctx, ops, fee, "a fee of ten percent", ""); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR moves a fee rate: %v", err)
	}
	// An ADMIN confirms what the preview showed; the change then waits.
	prev, err = h.svc.PreviewConfig(ctx, boss, fee)
	if err != nil || prev.Guard.Confirmation == nil || !prev.Guard.Confirmation.ExpiresAt.After(h.now) {
		t.Fatalf("admin's preview %+v %v", prev.Guard, err)
	}
	token := prev.Guard.Confirmation.Token
	if _, _, err := h.svc.ApplyConfig(ctx, boss, fee, "maker fee up", ""); code(err) != "ADMIN_CONFIRMATION_REQUIRED" {
		t.Fatalf("without the confirmation: %v", err)
	}
	if _, _, err := h.svc.ApplyConfig(ctx, deputy, fee, "maker fee up", token); code(err) != "ADMIN_CONFIRMATION_REQUIRED" {
		t.Fatalf("another administrator's confirmation: %v", err)
	}
	other := json.RawMessage(`{"fee_schedules":[{"tier":"default","maker_fee_rate":"0.2","taker_fee_rate":"0.001"}]}`)
	if _, _, err := h.svc.ApplyConfig(ctx, boss, other, "maker fee up", token); errDetail(err, "reason") != "changed" {
		t.Fatalf("a confirmation of another change: %v", err)
	}
	_, c, err := h.svc.ApplyConfig(ctx, boss, fee, "maker fee up", token)
	if err != nil || c == nil || c.Status != domain.ChangeScheduled || !c.EffectiveAt.Equal(h.now.Add(5*time.Minute)) || catalog.real != 1 {
		t.Fatalf("the change %+v %v (applied %d)", c, err, catalog.real)
	}
	if !slices.Contains(h.actions(), "admin.instruments.change_requested") {
		t.Fatalf("not audited: %v", h.actions())
	}
	if todo, err := h.svc.Todo(ctx, deputy); err != nil || todo.InstrumentChanges != 1 {
		t.Fatalf("todo %+v %v", todo, err)
	}
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 0 {
		t.Fatalf("too early: %d %v", n, err)
	}
	h.now = h.now.Add(5 * time.Minute)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 1 || catalog.real != 2 || catalog.items["FEE_SCHEDULE"]["default"]["maker_fee_rate"] != "0.1" {
		t.Fatalf("due: %d %v (applied %d)", n, err, catalog.real)
	}
	list, next, err := h.svc.InstrumentChanges(ctx, ops, "", "", 10)
	if err != nil || next != "" || len(list) != 1 || list[0].Status != domain.ChangeApplied || list[0].RequestedByEmail != "boss@example.com" ||
		list[0].Result != "1 changed, 0 unchanged" {
		t.Fatalf("changes %+v %q %v", list, next, err)
	}
	if last := h.store.audits[len(h.store.audits)-1]; last.GetAction() != "admin.instruments.change_applied" || last.GetActor() != "boss@example.com" {
		t.Fatalf("applied audit %v", last)
	}

	// A confirmation expires; a scheduled change can be canceled; one whose
	// items moved since fails.
	back := json.RawMessage(`{"fee_schedules":[{"tier":"default","maker_fee_rate":"0.001","taker_fee_rate":"0.001"}]}`)
	prev, _ = h.svc.PreviewConfig(ctx, boss, back)
	h.now = h.now.Add(11 * time.Minute)
	if _, _, err := h.svc.ApplyConfig(ctx, boss, back, "maker fee down", prev.Guard.Confirmation.Token); errDetail(err, "reason") != "expired" {
		t.Fatalf("an expired confirmation: %v", err)
	}
	prev, _ = h.svc.PreviewConfig(ctx, boss, back)
	_, c, err = h.svc.ApplyConfig(ctx, boss, back, "maker fee down", prev.Guard.Confirmation.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CancelInstrumentChange(ctx, ops, c.ID, "not now"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR cancels: %v", err)
	}
	canceled, err := h.svc.CancelInstrumentChange(ctx, deputy, c.ID, "wait for the weekend")
	if err != nil || canceled.Status != domain.ChangeCanceled || canceled.ClosedByEmail != "deputy@example.com" {
		t.Fatalf("canceled %+v %v", canceled, err)
	}
	if _, err := h.svc.CancelInstrumentChange(ctx, boss, c.ID, "again"); code(err) != "ADMIN_CHANGE_CLOSED" {
		t.Fatalf("a second cancel: %v", err)
	}
	prev, _ = h.svc.PreviewConfig(ctx, boss, back)
	_, c, _ = h.svc.ApplyConfig(ctx, boss, back, "maker fee down", prev.Guard.Confirmation.Token)
	catalog.items["FEE_SCHEDULE"]["default"]["taker_fee_rate"] = "0.002" // someone else got there first
	h.now = h.now.Add(5 * time.Minute)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 1 {
		t.Fatalf("due: %d %v", n, err)
	}
	if got, _ := h.store.Changes().Get(ctx, c.ID); got.Status != domain.ChangeFailed || !strings.Contains(got.Result, "preview it again") {
		t.Fatalf("a moved target %+v", got)
	}
}

func TestATwoPersonChangeWaitsForASecondAdmin(t *testing.T) {
	h, catalog, boss, deputy, _ := changeRig(t)
	ctx := context.Background()
	h.svc.Features = onFlags{flags.KeyTwoPerson: true}
	ref := json.RawMessage(`{"pairs":[{"symbol":"ETH-BTC","fee_tier":"default","reference_symbol":"ETHBTC","reference_multiplier":"2"}]}`)
	prev, err := h.svc.PreviewConfig(ctx, boss, ref)
	if err != nil || !prev.Guard.TwoPerson || len(prev.Guard.Params) != 1 || prev.Guard.Params[0].Field != "reference_multiplier" {
		t.Fatalf("preview %+v %v", prev.Guard, err)
	}
	_, c, err := h.svc.ApplyConfig(ctx, boss, ref, "double the reference", prev.Guard.Confirmation.Token)
	if err != nil || c.Status != domain.ChangePendingApproval || !c.EffectiveAt.IsZero() {
		t.Fatalf("change %+v %v", c, err)
	}
	if _, err := h.svc.DecideInstrumentChange(ctx, boss, c.ID, true, "mine"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("self approval: %v", err)
	}
	h.now = h.now.Add(time.Hour)
	if n, _ := h.svc.ApplyDueChanges(ctx); n != 0 {
		t.Fatal("applied before its approval")
	}
	approved, err := h.svc.DecideInstrumentChange(ctx, deputy, c.ID, true, "checked with Binance")
	if err != nil || approved.Status != domain.ChangeScheduled || approved.ApprovedByEmail != "deputy@example.com" ||
		!approved.EffectiveAt.Equal(h.now.Add(5*time.Minute)) {
		t.Fatalf("approved %+v %v", approved, err)
	}
	h.now = h.now.Add(5 * time.Minute)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 1 || catalog.items["TRADING_PAIR"]["ETH-BTC"]["reference_multiplier"] != "2" {
		t.Fatalf("due %d %v", n, err)
	}
	// A rejected one never takes effect.
	ref = json.RawMessage(`{"pairs":[{"symbol":"ETH-BTC","fee_tier":"default","reference_symbol":"ETHBTC","reference_multiplier":"3"}]}`)
	prev, _ = h.svc.PreviewConfig(ctx, boss, ref)
	_, c, _ = h.svc.ApplyConfig(ctx, boss, ref, "triple it", prev.Guard.Confirmation.Token)
	if rejected, err := h.svc.DecideInstrumentChange(ctx, deputy, c.ID, false, "not what Binance says"); err != nil || rejected.Status != domain.ChangeRejected {
		t.Fatalf("rejected %+v %v", rejected, err)
	}
}

func TestStatusChangesAndTheHalt(t *testing.T) {
	h, catalog, boss, _, ops := changeRig(t)
	ctx := context.Background()
	if _, err := h.svc.PreviewStatus(ctx, ops, domain.ChangePairStatus, "BTC-USDT", "HALT"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR halts: %v", err)
	}
	if _, err := h.svc.PreviewStatus(ctx, boss, domain.ChangePairStatus, "BTC-USDT", "PREPARE"); code(err) != "INSTRUMENT_STATUS_TRANSITION_INVALID" {
		t.Fatalf("back to PREPARE: %v", err)
	}
	// The halt is the emergency brake: at once, without a confirmation.
	prev, err := h.svc.PreviewStatus(ctx, boss, domain.ChangePairStatus, "btc-usdt", "halt")
	if err != nil || !prev.Immediate || prev.Confirmation != nil || prev.From != "TRADING" {
		t.Fatalf("halt preview %+v %v", prev, err)
	}
	res, err := h.svc.SetPairStatus(ctx, boss, "BTC-USDT", "HALT", "the feed is wrong", "")
	if err != nil || res.Change != nil || res.From != "TRADING" || catalog.items["TRADING_PAIR"]["BTC-USDT"]["status"] != "HALT" {
		t.Fatalf("halted %+v %v", res, err)
	}
	// Trading again waits like any change.
	prev, err = h.svc.PreviewStatus(ctx, boss, domain.ChangePairStatus, "BTC-USDT", "TRADING")
	if err != nil || prev.Immediate || prev.Confirmation == nil {
		t.Fatalf("resume preview %+v %v", prev, err)
	}
	if _, err := h.svc.SetPairStatus(ctx, boss, "BTC-USDT", "TRADING", "the feed is back", ""); code(err) != "ADMIN_CONFIRMATION_REQUIRED" {
		t.Fatalf("without the confirmation: %v", err)
	}
	if _, err := h.svc.SetContractStatus(ctx, boss, "BTC-USDT-PERP", "HALT", "wrong kind", prev.Confirmation.Token); err != nil {
		t.Fatalf("the contract halts at once: %v", err)
	}
	res, err = h.svc.SetPairStatus(ctx, boss, "BTC-USDT", "TRADING", "the feed is back", prev.Confirmation.Token)
	if err != nil || res.Change == nil || res.Change.Kind != domain.ChangePairStatus || res.Change.Target != "pair:BTC-USDT" {
		t.Fatalf("resume %+v %v", res, err)
	}
	h.now = h.now.Add(5 * time.Minute)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 1 || catalog.items["TRADING_PAIR"]["BTC-USDT"]["status"] != "TRADING" {
		t.Fatalf("due %d %v", n, err)
	}
	// A contract confirmed while trading, moved meanwhile: the change fails.
	cp, _ := h.svc.PreviewStatus(ctx, boss, domain.ChangeContractStatus, "BTC-USDT-PERP", "TRADING")
	res, err = h.svc.SetContractStatus(ctx, boss, "BTC-USDT-PERP", "TRADING", "back", cp.Confirmation.Token)
	if err != nil || res.Change == nil {
		t.Fatalf("contract %+v %v", res, err)
	}
	_, _ = catalog.setStatus("CONTRACT", "BTC-USDT-PERP", "CANCEL_ONLY")
	h.now = h.now.Add(5 * time.Minute)
	if _, err := h.svc.ApplyDueChanges(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.store.Changes().Get(ctx, res.Change.ID); got.Status != domain.ChangeFailed {
		t.Fatalf("a moved contract %+v", got)
	}
}

func TestLadderImpactsAndReferencesInUse(t *testing.T) {
	h, _, boss, _, ops := changeRig(t)
	ctx := context.Background()
	ladder := json.RawMessage(`{"contracts":[{"symbol":"BTC-USDT-PERP","index_symbol":"BTC-USDT","fee_tier":"default",
		"risk_tiers":[{"max_notional":"50000","max_leverage":20,"mmr":"0.04"}]}]}`)
	prev, err := h.svc.PreviewConfig(ctx, boss, ladder)
	if err != nil || len(prev.Guard.Impacts) != 1 || prev.Guard.Impacts[0].Liquidated != 3 || prev.Guard.Confirmation == nil ||
		len(h.derivatives.tiers) != 1 || !strings.Contains(h.derivatives.tiers[0], `"mmr":"0.04"`) {
		t.Fatalf("ladder preview %+v %v %v", prev.Guard, err, h.derivatives.tiers)
	}
	// Without its impact a ladder cannot be confirmed.
	h.derivatives.impactDown = true
	prev, err = h.svc.PreviewConfig(ctx, boss, ladder)
	if err != nil || prev.Guard.Confirmation != nil || !slices.Contains(prev.Warnings, ports.ConfigWarning{Code: WarnImpactUnknown, Symbol: "BTC-USDT-PERP"}) {
		t.Fatalf("unmeasured %+v %v", prev, err)
	}
	// HOUSE quotes BTC-USDT and the perpetual's index follows it: its
	// reference stays; LINK-USDT's may go (an ADMIN's change).
	cleared := func(symbol string) json.RawMessage {
		return json.RawMessage(`{"pairs":[{"symbol":"` + symbol + `","fee_tier":"default","reference_multiplier":"1"}]}`)
	}
	if _, err := h.svc.PreviewConfig(ctx, ops, cleared("BTC-USDT")); code(err) != "ADMIN_REFERENCE_IN_USE" {
		t.Fatalf("HOUSE's pair: %v", err)
	}
	h.svc.Flags = &fakeFlags{} // HOUSE off: the index still follows it
	if _, err := h.svc.PreviewConfig(ctx, ops, cleared("BTC-USDT")); errDetail(err, "used_by") != "BTC-USDT-PERP" {
		t.Fatalf("the index pair: %v", err)
	}
	prev, err = h.svc.PreviewConfig(ctx, ops, cleared("LINK-USDT"))
	if err != nil || len(prev.Guard.Params) != 1 || prev.Guard.Params[0].Field != "reference_symbol" || string(prev.Guard.Params[0].After) != "null" {
		t.Fatalf("LINK-USDT %+v %v", prev.Guard, err)
	}
	// A new pair touches nobody: an OPERATOR lists it at once.
	if _, c, err := h.svc.ApplyConfig(ctx, ops, json.RawMessage(`{"pairs":[{"symbol":"SOL-USDT","fee_tier":"default","reference_symbol":"",
		"reference_multiplier":"1"}]}`), "list SOL", ""); err != nil || c != nil {
		t.Fatalf("a new pair %+v %v", c, err)
	}
}

func TestTheChangeDelaySetting(t *testing.T) {
	h, _, boss, _, _ := changeRig(t)
	ctx := context.Background()
	short := 30 * time.Second
	if _, err := h.svc.UpdateSettings(ctx, boss, SettingsPatch{ChangeDelay: &short, Reason: "faster"}); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("30 seconds: %v", err)
	}
	two := 2 * time.Minute
	if v, err := h.svc.UpdateSettings(ctx, boss, SettingsPatch{ChangeDelay: &two, Reason: "the test server"}); err != nil || v.ChangeDelay != two {
		t.Fatalf("2 minutes: %+v %v", v, err)
	}
	prev, err := h.svc.PreviewStatus(ctx, boss, domain.ChangePairStatus, "ETH-BTC", "CANCEL_ONLY")
	if err != nil || prev.DelaySeconds != 120 {
		t.Fatalf("preview %+v %v", prev, err)
	}
	res, err := h.svc.SetPairStatus(ctx, boss, "ETH-BTC", "CANCEL_ONLY", "winding down", prev.Confirmation.Token)
	if err != nil || !res.Change.EffectiveAt.Equal(h.now.Add(two)) {
		t.Fatalf("scheduled %+v %v", res.Change, err)
	}
}

// errDetail is a detail of an apperr.Error.
func errDetail(err error, key string) string {
	var e *apperr.Error
	if !errors.As(err, &e) {
		return ""
	}
	v, _ := e.Details[key].(string)
	return v
}
