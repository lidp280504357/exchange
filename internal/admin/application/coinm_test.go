package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
)

// coinmCatalog lists contracts of both margin types, or fails to.
type coinmCatalog struct {
	launchCatalog
	contracts string
	err       error
}

func (c *coinmCatalog) List(context.Context) (json.RawMessage, error) {
	if c.err != nil {
		return nil, c.err
	}
	return json.RawMessage(`{"assets":[],"pairs":[],"contracts":` + c.contracts + `}`), nil
}

// coinmReports answers the read models' rows, which carry no settlement
// asset.
type coinmReports struct {
	ports.Reports
}

func (coinmReports) Liquidations(context.Context, ports.LiquidationQuery) ([]ports.LiquidationStep, string, error) {
	return []ports.LiquidationStep{{Symbol: "BTC-USD-PERP"}, {Symbol: "BTC-USDT-PERP"}, {Kind: "WARNING"}}, "", nil
}

func (coinmReports) Derivatives(context.Context, ports.ReportRange) ([]ports.DerivativesDay, error) {
	return []ports.DerivativesDay{{Symbol: "ETH-USD-PERP"}, {Symbol: "GONE-USDT-PERP"}}, nil
}

func (coinmReports) OpenInterest(context.Context) ([]ports.OpenInterest, error) {
	return []ports.OpenInterest{{Symbol: "BTC-USD-PERP"}}, nil
}

// coinmFlags lists the flags, or fails to.
type coinmFlags struct {
	launchFlags
	err error
}

func (f *coinmFlags) List(ctx context.Context) ([]ports.Flag, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.launchFlags.List(ctx)
}

// coinmLedger holds the insurance fund and the PnL clearing rows.
type coinmLedger struct {
	*fakeLedger
	funds, clearing map[string]string
}

// SystemBalances lists the clearing rows before the funds: the ledger
// promises no order.
func (l *coinmLedger) SystemBalances(context.Context, string) ([]ports.Balance, error) {
	out := []ports.Balance{{AccountType: "FEE_REVENUE", Asset: "USDT", Available: "7"}}
	for asset, v := range l.clearing {
		out = append(out, ports.Balance{AccountType: accountPnLClearing, Asset: asset, Available: v})
	}
	for asset, v := range l.funds {
		out = append(out, ports.Balance{AccountType: accountInsuranceFund, Asset: asset, Available: v})
	}
	return out, nil
}

const coinmContracts = `[
	{"symbol":"BTC-USDT-PERP","status":"TRADING","margin_type":"USDT","settle_asset":"USDT"},
	{"symbol":"ASTRA-USDT-PERP","status":"TRADING","margin_type":"USDT","settle_asset":"USDT"},
	{"symbol":"BTC-USD-PERP","status":"TRADING","margin_type":"COIN","settle_asset":"BTC"},
	{"symbol":"ETH-USD-PERP","status":"PREPARE","margin_type":"COIN","settle_asset":"ETH"},
	{"symbol":"OLD-USD-PERP","status":"DELISTED","margin_type":"COIN","settle_asset":"OLD"}]`

func TestInsuranceFundsBySettlementAsset(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	h.svc.Catalog = &coinmCatalog{contracts: coinmContracts}
	h.svc.Ledger = &coinmLedger{
		fakeLedger: h.ledger,
		funds:      map[string]string{"USDT": "1000334", "BTC": "2", "SOL": "5"},
		clearing:   map[string]string{"USDT": "-12.5", "BTC": "0.01", "XRP": "3", "SOL": "1"},
	}
	funds, err := h.svc.InsuranceFunds(ctx, auditor)
	if err != nil {
		t.Fatal(err)
	}
	// USDT first; the settlement assets of the contracts not delisted, and
	// SOL, which the fund holds; a clearing row alone (XRP) is no fund.
	if got := fmt.Sprint(funds); got != "[{USDT 1000334 -12.5} {BTC 2 0.01} {ETH 0 0} {SOL 5 1}]" {
		t.Fatalf("funds %s", got)
	}
}

func TestLaunchContracts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	fl := &coinmFlags{launchFlags: launchFlags{list: []ports.Flag{{Key: "derivatives.coin_m", Enabled: true}}}}
	ledger := &coinmLedger{fakeLedger: h.ledger, funds: map[string]string{"USDT": "1000"}}
	h.svc.Flags, h.svc.Catalog, h.svc.Ledger = fl, &coinmCatalog{contracts: coinmContracts}, ledger
	h.svc.Wallet, h.svc.Content, h.svc.Probe, h.svc.Platform = &launchWallet{}, &launchContent{}, &launchProbe{}, newFakePlatform()

	item := func(c LaunchChecklist, key string) LaunchItem { return c.Items[slicesIndex(c, key)] }
	c, err := h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip")
	if err != nil {
		t.Fatal(err)
	}
	// BTC-USD-PERP trades with no BTC in the fund; ETH-USD-PERP is not
	// open yet. derivatives.coin_m is on for everyone (the test server).
	ins := item(c, "insurance")
	if ins.Status != LaunchFail || fmt.Sprint(ins.Value["short"]) != "[BTC]" ||
		fmt.Sprint(ins.Value["balances"]) != "map[BTC:0 USDT:1000]" ||
		fmt.Sprint(ins.Value["contracts"]) != "map[BTC:[BTC-USD-PERP] USDT:[ASTRA-USDT-PERP BTC-USDT-PERP]]" ||
		fmt.Sprint(ins.Value["open"]) != "map[COIN:1 USDT:2]" {
		t.Fatalf("insurance %+v", ins)
	}
	if it := item(c, "coin_m"); it.Status != LaunchFail || it.Value["enabled"] != true {
		t.Fatalf("coin_m for everyone %+v", it)
	}

	ledger.funds["BTC"] = "2"
	fl.list = []ports.Flag{{Key: "derivatives.coin_m", Enabled: true, Rules: json.RawMessage(`{"regions":{"allow":["SG"]}}`)}}
	c, _ = h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip")
	if item(c, "insurance").Status != LaunchOK || item(c, "coin_m").Status != LaunchOK || item(c, "coin_m").Value["rules"] == nil {
		t.Fatalf("funded, open by rules %+v %+v", item(c, "insurance"), item(c, "coin_m"))
	}
	fl.list = []ports.Flag{{Key: "derivatives.coin_m"}}
	if c, _ = h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); item(c, "coin_m").Status != LaunchOK {
		t.Fatalf("off %+v", item(c, "coin_m"))
	}
	// Flags unknown: coin_m with them; the fund is the ledger's.
	fl.err = fmt.Errorf("config down")
	if c, _ = h.svc.LaunchChecklist(ctx, auditor, "admin.astras.vip"); item(c, "coin_m").Status != LaunchUnknown || item(c, "insurance").Status != LaunchOK {
		t.Fatalf("flags unknown %+v %+v", item(c, "coin_m"), item(c, "insurance"))
	}
}

func TestSettlementAssetsOfReadModels(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	catalog := &coinmCatalog{contracts: coinmContracts}
	h.svc.Catalog, h.svc.Reports = catalog, coinmReports{}
	read := func() string {
		steps, _, err := h.svc.Liquidations(ctx, auditor, ports.LiquidationQuery{})
		if err != nil {
			t.Fatal(err)
		}
		days, err := h.svc.DerivativesReport(ctx, auditor, ReportQuery{Days: 7})
		if err != nil {
			t.Fatal(err)
		}
		oi, err := h.svc.OpenInterest(ctx, auditor)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%q %q %q", []string{steps[0].SettleAsset, steps[1].SettleAsset, steps[2].SettleAsset},
			[]string{days[0].SettleAsset, days[1].SettleAsset}, oi[0].SettleAsset)
	}
	// A cross account's warning names no contract; a contract no longer
	// listed has none.
	if got := read(); got != `["BTC" "USDT" ""] ["ETH" ""] "BTC"` {
		t.Fatalf("settlement assets %s", got)
	}
	// The listing unknown: the rows as they are, read as USDT.
	catalog.err = errors.New("instruments down")
	if got := read(); got != `["" "" ""] ["" ""] ""` {
		t.Fatalf("listing unknown %s", got)
	}
}
