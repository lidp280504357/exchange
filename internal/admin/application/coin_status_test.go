package application

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/flags"
)

// coinDoc lists BTC's and ETH's contracts of both margin types, a coin
// whose code BTC begins, and a delisted one.
const coinDoc = `{
	"fee_schedules": [{"tier":"default","maker_fee_rate":"0.001","taker_fee_rate":"0.001"}],
	"contracts": [
		{"symbol":"BTC-USDT-PERP","base_asset":"BTC","margin_type":"USDT","fee_tier":"default","status":"TRADING"},
		{"symbol":"BTC-USD-PERP","base_asset":"BTC","margin_type":"COIN","fee_tier":"default","status":"HALT"},
		{"symbol":"BTCDOM-USDT-PERP","base_asset":"BTCDOM","margin_type":"USDT","fee_tier":"default","status":"TRADING"},
		{"symbol":"ETH-USDT-PERP","base_asset":"ETH","margin_type":"USDT","fee_tier":"default","status":"TRADING"},
		{"symbol":"ETH-USD-PERP","base_asset":"ETH","margin_type":"COIN","fee_tier":"default","status":"PREPARE"},
		{"symbol":"LUNA-USDT-PERP","base_asset":"LUNA","fee_tier":"default","status":"DELISTED"}]}`

func TestCloseAndReopenACoinsContracts(t *testing.T) {
	h, catalog, boss, deputy, ops := changeRigOf(t, coinDoc)
	ctx := context.Background()
	status := func(symbol string) any { return catalog.items["CONTRACT"][symbol]["status"] }
	if _, err := h.svc.PreviewCoinStatus(ctx, ops, "BTC", "CANCEL_ONLY"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR closes a coin: %v", err)
	}
	for _, bad := range []struct{ coin, to, code string }{
		{"BTC", "DELISTED", apperr.CodeInvalidArgument}, // delisting stays per contract
		{"B-TC", "CANCEL_ONLY", apperr.CodeInvalidArgument},
		{"DOGE", "CANCEL_ONLY", apperr.CodeNotFound},
		{"LUNA", "CANCEL_ONLY", apperr.CodeNotFound},               // delisted only
		{"BTC", "TRADING", "INSTRUMENT_STATUS_TRANSITION_INVALID"}, // none closed
		{"ETH", "TRADING", "INSTRUMENT_STATUS_TRANSITION_INVALID"}, // one in preparation opens on its own
		{"BTCDOM", "TRADING", "INSTRUMENT_STATUS_TRANSITION_INVALID"},
	} {
		if _, err := h.svc.PreviewCoinStatus(ctx, boss, bad.coin, bad.to); code(err) != bad.code {
			t.Fatalf("%s to %s: %v", bad.coin, bad.to, err)
		}
	}

	// Closing BTC takes both margin types, the halted one too; BTCDOM is
	// another coin's.
	prev, err := h.svc.PreviewCoinStatus(ctx, boss, " btc ", "cancel_only")
	if err != nil || prev.Coin != "BTC" || prev.To != "CANCEL_ONLY" || prev.Confirmation == nil || prev.DelaySeconds != 300 ||
		fmt.Sprint(prev.Contracts) != "[{BTC-USD-PERP COIN HALT CANCEL_ONLY} {BTC-USDT-PERP USDT TRADING CANCEL_ONLY}]" || len(prev.Staying) != 0 {
		t.Fatalf("close BTC %+v %v", prev, err)
	}
	if _, err := h.svc.SetCoinStatus(ctx, boss, "BTC", "CANCEL_ONLY", "winding BTC down", ""); code(err) != "ADMIN_CONFIRMATION_REQUIRED" {
		t.Fatalf("without the confirmation: %v", err)
	}
	eth, err := h.svc.PreviewCoinStatus(ctx, boss, "ETH", "CANCEL_ONLY")
	if err != nil || fmt.Sprint(eth.Contracts) != "[{ETH-USDT-PERP USDT TRADING CANCEL_ONLY}]" ||
		fmt.Sprint(eth.Staying) != "[{ETH-USD-PERP COIN PREPARE PREPARE}]" {
		t.Fatalf("close ETH %+v %v", eth, err)
	}
	if _, err := h.svc.SetCoinStatus(ctx, boss, "BTC", "CANCEL_ONLY", "winding BTC down", eth.Confirmation.Token); errDetail(err, "reason") != "changed" {
		t.Fatalf("ETH's confirmation closes BTC: %v", err)
	}
	if _, err := h.svc.SetCoinStatus(ctx, deputy, "BTC", "CANCEL_ONLY", "winding BTC down", prev.Confirmation.Token); code(err) != "ADMIN_CONFIRMATION_REQUIRED" {
		t.Fatalf("another administrator's confirmation: %v", err)
	}
	res, err := h.svc.SetCoinStatus(ctx, boss, "BTC", "CANCEL_ONLY", "winding BTC down", prev.Confirmation.Token)
	if err != nil || res.Change.Kind != domain.ChangeCoinContractsStatus || res.Change.Target != "coin:BTC" ||
		res.Change.Status != domain.ChangeScheduled || len(res.Contracts) != 2 {
		t.Fatalf("closing %+v %v", res, err)
	}
	var sum changeSummary
	if err := json.Unmarshal(res.Change.Summary, &sum); err != nil || len(sum.Params) != 2 || sum.Params[0].Key != "BTC-USD-PERP" ||
		sum.Params[0].Field != "status" || string(sum.Params[0].Before) != `"HALT"` || string(sum.Params[1].After) != `"CANCEL_ONLY"` {
		t.Fatalf("what it moves %+v %v", sum.Params, err)
	}
	if again, err := h.svc.SetCoinStatus(ctx, boss, "BTC", "CANCEL_ONLY", "winding BTC down", prev.Confirmation.Token); err != nil ||
		again.Change.ID != res.Change.ID || len(again.Contracts) != 2 {
		t.Fatalf("the confirmation again %+v %v", again, err)
	}

	// Nothing moves before its time; then each contract, each audited as
	// its own status move.
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 0 || status("BTC-USDT-PERP") != "TRADING" {
		t.Fatalf("early %d %v", n, err)
	}
	h.now = h.now.Add(5 * time.Minute)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 1 || status("BTC-USDT-PERP") != "CANCEL_ONLY" ||
		status("BTC-USD-PERP") != "CANCEL_ONLY" || status("BTCDOM-USDT-PERP") != "TRADING" {
		t.Fatalf("due %d %v", n, err)
	}
	got, _ := h.store.Changes().Get(ctx, res.Change.ID)
	if got.Status != domain.ChangeApplied || got.Result != "BTC-USD-PERP: HALT → CANCEL_ONLY; BTC-USDT-PERP: TRADING → CANCEL_ONLY" {
		t.Fatalf("closed %+v", got)
	}
	moved := map[string]string{}
	for _, e := range h.store.audits {
		if e.GetAction() == "admin.instruments.contract_status" {
			moved[e.GetTarget()] = e.GetDetails()
		}
	}
	if len(moved) != 2 || moved["contract:BTC-USD-PERP"] != `{"change_id":"`+res.Change.ID+`","coin":"BTC","from":"HALT","to":"CANCEL_ONLY"}` {
		t.Fatalf("audited %v", moved)
	}

	// Reopened: those closed go back to trading (B122), one found trading
	// already is in effect; a move the service refuses is reported with
	// the others.
	prev, err = h.svc.PreviewCoinStatus(ctx, boss, "BTC", "TRADING")
	if err != nil || fmt.Sprint(prev.Contracts) != "[{BTC-USD-PERP COIN CANCEL_ONLY TRADING} {BTC-USDT-PERP USDT CANCEL_ONLY TRADING}]" {
		t.Fatalf("reopen BTC %+v %v", prev, err)
	}
	res, err = h.svc.SetCoinStatus(ctx, boss, "BTC", "TRADING", "BTC stays", prev.Confirmation.Token)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = catalog.setStatus("CONTRACT", "BTC-USDT-PERP", "TRADING")
	catalog.refuse = "BTC-USD-PERP"
	h.now = h.now.Add(5 * time.Minute)
	if _, err := h.svc.ApplyDueChanges(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = h.store.Changes().Get(ctx, res.Change.ID)
	if got.Status != domain.ChangeFailed || got.Result != "COMMON_CONFLICT: 1 of 2 did not move: BTC-USD-PERP: CANCEL_ONLY → TRADING "+
		"failed: INSTRUMENT_STATUS_TRANSITION_INVALID: no such move; BTC-USDT-PERP: CANCEL_ONLY → TRADING (in effect already)" {
		t.Fatalf("partly %+v", got)
	}
	catalog.refuse = ""

	// A contract moved elsewhere since the confirmation: none moves.
	prev, _ = h.svc.PreviewCoinStatus(ctx, boss, "BTC", "TRADING")
	res, err = h.svc.SetCoinStatus(ctx, boss, "BTC", "TRADING", "BTC stays", prev.Confirmation.Token)
	if err != nil || len(res.Contracts) != 1 {
		t.Fatalf("reopen the rest %+v %v", res, err)
	}
	_, _ = catalog.setStatus("CONTRACT", "BTC-USD-PERP", "DELISTED")
	h.now = h.now.Add(5 * time.Minute)
	if _, err := h.svc.ApplyDueChanges(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = h.store.Changes().Get(ctx, res.Change.ID); got.Status != domain.ChangeFailed || status("BTC-USD-PERP") != "DELISTED" ||
		got.Result != "COMMON_CONFLICT: the contracts changed since the change was confirmed, nothing more moves; preview it again: "+
			"BTC-USD-PERP: not moved (DELISTED now)" {
		t.Fatalf("moved meanwhile %+v", got)
	}
}

// A contract moved elsewhere between two rounds of a coin's change fails
// it, the result saying which of its contracts are where they go and
// which are not (review EY ①).
func TestACoinsChangeFailingAfterARound(t *testing.T) {
	h, catalog, boss, _, _ := changeRigOf(t, coinDoc)
	ctx := context.Background()
	prev, err := h.svc.PreviewCoinStatus(ctx, boss, "BTC", "CANCEL_ONLY")
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.SetCoinStatus(ctx, boss, "BTC", "CANCEL_ONLY", "winding BTC down", prev.Confirmation.Token)
	if err != nil {
		t.Fatal(err)
	}
	// BTC-USD-PERP moves; BTC-USDT-PERP's move fails for a moment.
	h.now = h.now.Add(5 * time.Minute)
	catalog.unavailable = "BTC-USDT-PERP"
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 0 || catalog.items["CONTRACT"]["BTC-USD-PERP"]["status"] != "CANCEL_ONLY" {
		t.Fatalf("the first round %d %v", n, err)
	}
	// Meanwhile BTC-USDT-PERP is halted by hand.
	catalog.unavailable = ""
	_, _ = catalog.setStatus("CONTRACT", "BTC-USDT-PERP", "HALT")
	h.now = h.now.Add(domain.ClaimHold)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 1 {
		t.Fatalf("the next round %d %v", n, err)
	}
	got, _ := h.store.Changes().Get(ctx, res.Change.ID)
	if got.Status != domain.ChangeFailed || got.Result != "COMMON_CONFLICT: the contracts changed since the change was confirmed, "+
		"nothing more moves; preview it again: BTC-USD-PERP: HALT → CANCEL_ONLY (in effect already); BTC-USDT-PERP: not moved (HALT now)" {
		t.Fatalf("failed %+v", got)
	}
	if catalog.items["CONTRACT"]["BTC-USDT-PERP"]["status"] != "HALT" {
		t.Fatal("a contract moved by hand was moved again")
	}
}

// Two-person (review EY ②): a coin's change waits for a second ADMIN, not
// its requester; approved, it waits the delay and moves the contracts;
// rejected, it never does.
func TestACoinsChangeWithTwoPeople(t *testing.T) {
	h, catalog, boss, deputy, _ := changeRigOf(t, coinDoc)
	ctx := context.Background()
	h.svc.Features = onFlags{flags.KeyTwoPerson: true}
	status := func(symbol string) any { return catalog.items["CONTRACT"][symbol]["status"] }
	prev, err := h.svc.PreviewCoinStatus(ctx, boss, "ETH", "CANCEL_ONLY")
	if err != nil || !prev.TwoPerson || len(prev.Contracts) != 1 {
		t.Fatalf("preview %+v %v", prev, err)
	}
	res, err := h.svc.SetCoinStatus(ctx, boss, "ETH", "CANCEL_ONLY", "winding ETH down", prev.Confirmation.Token)
	if err != nil || res.Change.Status != domain.ChangePendingApproval || !res.Change.EffectiveAt.IsZero() {
		t.Fatalf("closing %+v %v", res.Change, err)
	}
	h.now = h.now.Add(time.Hour)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 0 || status("ETH-USDT-PERP") != "TRADING" {
		t.Fatalf("applied unapproved %d %v", n, err)
	}
	if _, err := h.svc.DecideInstrumentChange(ctx, boss, res.Change.ID, true, "my own"); code(err) != "ADMIN_SELF_APPROVAL" {
		t.Fatalf("approved by its requester: %v", err)
	}
	approved, err := h.svc.DecideInstrumentChange(ctx, deputy, res.Change.ID, true, "checked the positions")
	if err != nil || approved.Status != domain.ChangeScheduled || !approved.EffectiveAt.Equal(h.now.Add(5*time.Minute)) {
		t.Fatalf("approved %+v %v", approved, err)
	}
	h.now = h.now.Add(5 * time.Minute)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 1 || status("ETH-USDT-PERP") != "CANCEL_ONLY" || status("ETH-USD-PERP") != "PREPARE" {
		t.Fatalf("due %d %v", n, err)
	}
	// Reopening, rejected: nothing moves.
	prev, _ = h.svc.PreviewCoinStatus(ctx, boss, "ETH", "TRADING")
	res, err = h.svc.SetCoinStatus(ctx, boss, "ETH", "TRADING", "ETH back", prev.Confirmation.Token)
	if err != nil {
		t.Fatal(err)
	}
	if rejected, err := h.svc.DecideInstrumentChange(ctx, deputy, res.Change.ID, false, "not yet"); err != nil || rejected.Status != domain.ChangeRejected {
		t.Fatalf("rejected %+v %v", rejected, err)
	}
	h.now = h.now.Add(time.Hour)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 0 || status("ETH-USDT-PERP") != "CANCEL_ONLY" {
		t.Fatalf("a rejected change applied %d %v", n, err)
	}
}

// A coin's change waits when instrument-service is down, and when the
// answer of a move is lost it stays claimed and is checked after the
// claim's hold: the moved one is in effect, the rest moves.
func TestACoinsChangeAcrossRounds(t *testing.T) {
	h, catalog, boss, _, _ := changeRigOf(t, coinDoc)
	ctx := context.Background()
	prev, err := h.svc.PreviewCoinStatus(ctx, boss, "BTC", "CANCEL_ONLY")
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.SetCoinStatus(ctx, boss, "BTC", "CANCEL_ONLY", "winding BTC down", prev.Confirmation.Token)
	if err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(5 * time.Minute)
	catalog.down = true
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 0 {
		t.Fatalf("down %d %v", n, err)
	}
	catalog.down, catalog.lost = false, true
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 0 {
		t.Fatalf("lost %d %v", n, err)
	}
	got, _ := h.store.Changes().Get(ctx, res.Change.ID)
	if got.Status != domain.ChangeScheduled || got.ApplyingAt.IsZero() || catalog.items["CONTRACT"]["BTC-USD-PERP"]["status"] != "CANCEL_ONLY" ||
		catalog.items["CONTRACT"]["BTC-USDT-PERP"]["status"] != "TRADING" {
		t.Fatalf("claimed, the first moved %+v", got)
	}
	catalog.lost = false
	h.now = h.now.Add(domain.ClaimHold)
	if n, err := h.svc.ApplyDueChanges(ctx); err != nil || n != 1 {
		t.Fatalf("next round %d %v", n, err)
	}
	got, _ = h.store.Changes().Get(ctx, res.Change.ID)
	if got.Status != domain.ChangeApplied ||
		got.Result != "BTC-USD-PERP: HALT → CANCEL_ONLY (in effect already); BTC-USDT-PERP: TRADING → CANCEL_ONLY" {
		t.Fatalf("applied %+v", got)
	}
}

// B122: a closed pair or contract reopens until it is delisted.
func TestReopenUntilDelisted(t *testing.T) {
	h, catalog, boss, _, _ := changeRig(t)
	ctx := context.Background()
	_, _ = catalog.setStatus("CONTRACT", "BTC-USDT-PERP", "CANCEL_ONLY")
	prev, err := h.svc.PreviewStatus(ctx, boss, domain.ChangeContractStatus, "BTC-USDT-PERP", "TRADING")
	if err != nil || prev.From != "CANCEL_ONLY" || prev.Confirmation == nil {
		t.Fatalf("reopen %+v %v", prev, err)
	}
	_, _ = catalog.setStatus("CONTRACT", "BTC-USDT-PERP", "DELISTED")
	if _, err := h.svc.PreviewStatus(ctx, boss, domain.ChangeContractStatus, "BTC-USDT-PERP", "TRADING"); code(err) != "INSTRUMENT_STATUS_TRANSITION_INVALID" {
		t.Fatalf("delisted is final: %v", err)
	}
}
