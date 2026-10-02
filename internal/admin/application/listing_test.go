package application

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// fakeCatalog holds a config document; Apply records what it was asked.
type fakeCatalog struct {
	ports.Instruments
	applied []string
}

func (c *fakeCatalog) Export(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"fee_schedules":[],"assets":[],"pairs":[{"symbol":"BTC-USDT","reference_symbol":"BTCUSDT"},
		{"symbol":"LINK-USDT","reference_symbol":"LINKUSDT"}],"contracts":[{"symbol":"BTC-USDT-PERP","index_symbol":"BTC-USDT"}]}`), nil
}

func (c *fakeCatalog) Apply(_ context.Context, config json.RawMessage, dryRun bool, actor, reason string) (ports.ConfigResult, error) {
	c.applied = append(c.applied, strings.Join([]string{actor, reason, map[bool]string{true: "dry", false: "real"}[dryRun]}, " "))
	return ports.ConfigResult{
		Changes: []ports.ConfigChange{{Entity: "TRADING_PAIR", Key: "LINK-BTC", Action: "CREATE", Version: 1, Before: json.RawMessage("null"), After: config}},
	}, nil
}

// fakeReference lists BTCUSDT on both markets and LINKBTC on spot only.
type fakeReference struct{ asked []string }

func (r *fakeReference) Listed(_ context.Context, symbol string) (bool, bool, error) {
	r.asked = append(r.asked, symbol)
	return symbol == "BTCUSDT" || symbol == "LINKBTC" || symbol == "LINKUSDT", symbol == "BTCUSDT" || symbol == "LINKUSDT", nil
}

type houseFlags struct{ fakeFlags }

func (*houseFlags) List(context.Context) ([]ports.Flag, error) {
	return []ports.Flag{{Key: houseFlag, Enabled: true, Rules: json.RawMessage(`{"symbols":{"allow":["BTC-USDT","BTC-USDT-PERP"]}}`)}}, nil
}

func TestEditingTheReferenceData(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	catalog, ref := &fakeCatalog{}, &fakeReference{}
	h.svc.Catalog, h.svc.Reference, h.svc.Flags = catalog, ref, &houseFlags{}
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "audit@example.com", domain.RoleAuditor)
	ops, auditor := h.login(t, "ops@example.com"), h.login(t, "audit@example.com")

	if raw, err := h.svc.InstrumentConfig(ctx, auditor); err != nil || !strings.Contains(string(raw), "BTC-USDT-PERP") {
		t.Fatalf("auditors read the document: %s %v", raw, err)
	}
	link := json.RawMessage(`{"pairs":[{"symbol":"LINK-BTC","reference_symbol":"linkbtc"}]}`)
	if _, err := h.svc.PreviewConfig(ctx, auditor, link); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("auditors change nothing: %v", err)
	}
	for _, bad := range []string{``, `[`, strings.Repeat(" ", maxConfig+1)} {
		if _, err := h.svc.PreviewConfig(ctx, ops, json.RawMessage(bad)); code(err) != apperr.CodeInvalidArgument {
			t.Fatalf("%.10q: %v", bad, err)
		}
	}

	// A new pair: its symbol checked, HOUSE's list and the reconnect noted.
	res, err := h.svc.PreviewConfig(ctx, ops, link)
	if err != nil || len(res.Changes) != 1 || len(res.Warnings) != 2 || res.Warnings[0].Code != ports.WarnHouseNotListed || res.Warnings[0].Symbol != "LINK-BTC" ||
		res.Warnings[1].Code != ports.WarnStreamsReconnect {
		t.Fatalf("preview %+v %v", res, err)
	}
	if !slices.Equal(ref.asked, []string{"LINKBTC"}) || catalog.applied[0] != "ops@example.com preview dry" {
		t.Fatalf("asked %v, applied %v", ref.asked, catalog.applied)
	}
	// A symbol Binance does not list is refused before anything is asked.
	_, err = h.svc.PreviewConfig(ctx, ops, json.RawMessage(`{"pairs":[{"symbol":"NOPE-BTC","reference_symbol":"NOPEBTC"}]}`))
	if code(err) != "ADMIN_REFERENCE_UNKNOWN" || len(catalog.applied) != 1 {
		t.Fatalf("unknown symbol: %v %v", err, catalog.applied)
	}
	// Unchanged references are not asked again; a new contract on an
	// index without futures is noted.
	ref.asked = nil
	res, err = h.svc.PreviewConfig(ctx, ops, json.RawMessage(`{"pairs":[{"symbol":"BTC-USDT","reference_symbol":"BTCUSDT"}],
		"contracts":[{"symbol":"LINK-BTC-PERP","index_symbol":"LINK-BTC"},{"symbol":"LINK-USDT-PERP","index_symbol":"LINK-USDT"}]}`))
	if err != nil || len(ref.asked) != 1 || ref.asked[0] != "LINKUSDT" {
		t.Fatalf("asked %v %v", ref.asked, err)
	}
	if !slices.Contains(res.Warnings, ports.ConfigWarning{Code: ports.WarnNoIndexReference, Symbol: "LINK-BTC-PERP", Detail: "LINK-BTC"}) ||
		!slices.Contains(res.Warnings, ports.ConfigWarning{Code: ports.WarnHouseNotListed, Symbol: "LINK-USDT-PERP", Detail: houseFlag}) {
		t.Fatalf("contract notes %v", res.Warnings)
	}

	if _, _, err := h.svc.ApplyConfig(ctx, ops, link, "", ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	if _, c, err := h.svc.ApplyConfig(ctx, ops, link, "list LINK against BTC", ""); err != nil || c != nil {
		t.Fatal(err)
	}
	if catalog.applied[len(catalog.applied)-1] != "ops@example.com list LINK against BTC real" || !slices.Contains(h.actions(), "admin.instruments.applied") {
		t.Fatalf("applied %v, audit %v", catalog.applied, h.actions())
	}
}
