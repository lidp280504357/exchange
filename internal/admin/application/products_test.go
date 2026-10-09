package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// productFlags keeps flags as the config store does: a switch stores the
// flag at a new version with who and when; afterSwitch runs once one is
// stored (a caller going away then).
type productFlags struct {
	flags       map[string]ports.Flag
	now         func() time.Time
	down        bool
	afterSwitch func()
	// changes are each flag's switches, newest first (flag_changes).
	changes map[string][]ports.FlagChange
}

func (f *productFlags) History(_ context.Context, key string, limit int) ([]ports.FlagChange, error) {
	return f.changes[key][:min(limit, len(f.changes[key]))], nil
}

func (f *productFlags) List(context.Context) ([]ports.Flag, error) {
	if f.down {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "config down")
	}
	out := []ports.Flag{{Key: "wallet.withdraw"}}
	for _, v := range f.flags {
		out = append(out, v)
	}
	return out, nil
}

func (f *productFlags) Switch(_ context.Context, key string, enabled bool, actor, _ string) (ports.Flag, error) {
	at := f.now()
	v := f.flags[key]
	v.Key, v.Enabled, v.UpdatedBy, v.UpdatedAt = key, enabled, actor, &at
	v.Version++
	f.flags[key] = v
	if f.changes == nil {
		f.changes = map[string][]ports.FlagChange{}
	}
	f.changes[key] = append([]ports.FlagChange{{Enabled: enabled, At: at, By: actor}}, f.changes[key]...)
	if f.afterSwitch != nil {
		f.afterSwitch()
	}
	return v, nil
}

// productServices answer as the services that run the lines: their counts
// (or an error by line) and the cancels asked of them.
type productServices struct {
	lines  map[string]ports.ProductLine
	errs   map[string]error
	asked  []string
	cancel ports.ProductCanceled
	err    error
}

func (s *productServices) Line(_ context.Context, product string) (ports.ProductLine, error) {
	if err := s.errs[product]; err != nil {
		return ports.ProductLine{}, err
	}
	return s.lines[product], nil
}

func (s *productServices) CancelOpen(_ context.Context, product, actor, reason string) (ports.ProductCanceled, error) {
	s.asked = append(s.asked, product+" by "+actor+": "+reason)
	return s.cancel, s.err
}

// The product lines from the console (design 2026-10-07, product switches,
// K3): each line's switch and what closing it touches, as its service
// counts it; closing one cancels its orders through its service, the
// answer saying how that went (A85), opening cancels nothing, the state it
// is in changes nothing - but a closed line's orders still open are
// canceled on closing it again; the flags page leaves the lines to their
// card; the checklist names what is open.
func TestProductLines(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "ops@example.com", domain.RoleOperator)
	h.admin(t, "aud@example.com", domain.RoleAuditor)
	boss, ops, aud := h.login(t, "boss@example.com"), h.login(t, "ops@example.com"), h.login(t, "aud@example.com")
	seeded := h.now.Add(-time.Hour)
	pf := &productFlags{now: func() time.Time { return h.now }, flags: map[string]ports.Flag{
		"product.spot":   {Key: "product.spot", Enabled: true, Version: 1, UpdatedBy: "migration config 00002", UpdatedAt: &seeded},
		"product.usdt_m": {Key: "product.usdt_m", Enabled: true, Version: 1, UpdatedBy: "migration config 00002", UpdatedAt: &seeded},
	}}
	svcs := &productServices{cancel: ports.ProductCanceled{Orders: 4}, lines: map[string]ports.ProductLine{
		"spot": {OpenOrders: 4}, "usdt_m": {OpenOrders: 7, OpenPositions: 7}, "coin_m": {OpenOrders: 5, OpenPositions: 5},
	}}
	h.svc.Flags, h.svc.ProductLines = pf, svcs

	// Every administrator reads them: spot and the USDT-margined line as
	// seeded, the coin-margined one not stored, open.
	ps, err := h.svc.Products(ctx, aud)
	if err != nil || len(ps.Lines) != 3 || len(ps.Partial) != 0 {
		t.Fatalf("read %+v %v", ps, err)
	}
	spot, usdtM, coinM := ps.Lines[0], ps.Lines[1], ps.Lines[2]
	if spot.Product != "spot" || !spot.Enabled || spot.Version != 1 || spot.SwitchedBy != "migration config 00002" || spot.ClosedAt != nil ||
		*spot.OpenOrders != 4 || *spot.OpenPositions != 0 {
		t.Fatalf("spot %+v", spot)
	}
	if usdtM.Product != "usdt_m" || *usdtM.OpenOrders != 7 || *usdtM.OpenPositions != 7 {
		t.Fatalf("usdt_m %+v", usdtM)
	}
	if coinM.Product != "coin_m" || !coinM.Enabled || coinM.Version != 0 || coinM.SwitchedAt != nil || *coinM.OpenOrders != 5 || *coinM.OpenPositions != 5 {
		t.Fatalf("coin_m %+v", coinM)
	}

	// Only an ADMIN switches a line, with a reason, among the three.
	if _, err := h.svc.SetProduct(ctx, ops, "coin_m", false, "close coin-margined"); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR: %v", err)
	}
	if _, err := h.svc.SetProduct(ctx, boss, "margin", false, "close margin"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("not a line: %v", err)
	}
	if _, err := h.svc.SetProduct(ctx, boss, "coin_m", false, ""); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("no reason: %v", err)
	}

	// Closing: switched, its orders canceled through its service, audited.
	ps, err = h.svc.SetProduct(ctx, boss, " COIN_M ", false, "close coin-margined for the drill")
	if err != nil || ps.Cancel == nil || *ps.Cancel != (ProductCancel{Status: CancelDone, Canceled: 4}) || ps.Lines[2].Enabled || ps.Lines[2].ClosedAt == nil || !ps.Lines[2].ClosedAt.Equal(h.now) ||
		ps.Lines[2].SwitchedBy != "boss@example.com" || ps.Lines[2].Version != 1 {
		t.Fatalf("closed %+v %v", ps, err)
	}
	if len(svcs.asked) != 1 || svcs.asked[0] != "coin_m by boss@example.com: close coin-margined for the drill" {
		t.Fatalf("cancels asked %v", svcs.asked)
	}
	if got := h.auditsOf("admin.products.toggled"); len(got) != 1 || !strings.HasPrefix(got[0], "product:coin_m close coin-margined for the drill ") ||
		!strings.Contains(got[0], `"canceled_orders":4`) || !strings.Contains(got[0], `"from":true`) || !strings.Contains(got[0], `"to":false`) {
		t.Fatalf("audited %v", got)
	}
	// Closed already: nothing changes while no order is open...
	svcs.lines["coin_m"] = ports.ProductLine{OpenPositions: 5}
	if ps, err := h.svc.SetProduct(ctx, boss, "coin_m", false, "close it again"); err != nil || ps.Cancel != nil || len(svcs.asked) != 1 ||
		pf.flags["product.coin_m"].Version != 1 {
		t.Fatalf("closed again %+v %v %v", ps, err, svcs.asked)
	}
	// ...but orders still open (a cancel that failed) are canceled again.
	svcs.lines["coin_m"] = ports.ProductLine{OpenOrders: 2, OpenPositions: 5}
	svcs.cancel = ports.ProductCanceled{Orders: 2}
	if ps, err := h.svc.SetProduct(ctx, boss, "coin_m", false, "cancel what is left"); err != nil || ps.Cancel == nil ||
		*ps.Cancel != (ProductCancel{Status: CancelDone, Canceled: 2}) || len(svcs.asked) != 2 ||
		pf.flags["product.coin_m"].Version != 1 || len(h.auditsOf("admin.products.orders_canceled")) != 1 {
		t.Fatalf("its orders left %+v %v %v", ps, err, svcs.asked)
	}

	// Opening cancels nothing; the switch stands though a cancel fails,
	// the audit saying why (a service without the endpoint yet, or down).
	if ps, err := h.svc.SetProduct(ctx, boss, "coin_m", true, "open coin-margined again"); err != nil || !ps.Lines[2].Enabled ||
		ps.Lines[2].ClosedAt != nil || ps.Cancel != nil || len(svcs.asked) != 2 {
		t.Fatalf("opened %+v %v", ps, err)
	}
	svcs.err, svcs.cancel = ports.ErrProductLineMissing, ports.ProductCanceled{}
	if ps, err := h.svc.SetProduct(ctx, boss, "spot", false, "close spot"); err != nil || ps.Lines[0].Enabled || ps.Cancel == nil ||
		*ps.Cancel != (ProductCancel{Status: CancelUnavailable}) {
		t.Fatalf("closed without the service's cancel %+v %v", ps, err)
	}
	if got := h.auditsOf("admin.products.toggled"); len(got) != 3 || !strings.Contains(got[2], `"cancel":"unavailable"`) {
		t.Fatalf("audited %v", got)
	}
	// A refusal says the service's code and message; a cancel past its
	// time or not reached says only that, in the answer and the audit alike
	// (A87, A88: the console shows both, no address of ours; the log keeps
	// the failure in full).
	svcs.err = apperr.New(apperr.KindConflict, apperr.CodeConflict, "the product line is open")
	if _, err := h.svc.SetProduct(ctx, boss, "spot", true, "open spot"); err != nil {
		t.Fatal(err)
	}
	ps, err = h.svc.SetProduct(ctx, boss, "spot", false, "close spot again")
	if err != nil || ps.Cancel == nil || *ps.Cancel != (ProductCancel{Status: CancelFailed, Reason: CancelRefused, Error: "COMMON_CONFLICT: the product line is open"}) {
		t.Fatalf("a refused cancel %+v %v", ps.Cancel, err)
	}
	if got := h.auditsOf("admin.products.toggled"); !strings.Contains(got[len(got)-1], `"cancel":"failed"`) ||
		!strings.Contains(got[len(got)-1], `"cancel_reason":"REFUSED"`) || !strings.Contains(got[len(got)-1], "the product line is open") {
		t.Fatalf("a refused cancel audited %v", got)
	}
	inner := `Post "http://spot-trading-service:8088/internal/products/spot/cancel-open": context deadline exceeded`
	for _, tc := range []struct {
		sentinel error
		reason   string
	}{{ports.ErrProductCancelTimeout, CancelTimeout}, {ports.ErrProductCancelUnreachable, CancelUnreachable}} {
		svcs.err = fmt.Errorf("%w: %w", tc.sentinel, apperr.Wrap(errors.New(inner), apperr.KindUnavailable, apperr.CodeUnavailable, "service unreachable"))
		svcs.lines["spot"] = ports.ProductLine{OpenOrders: 1}
		ps, err = h.svc.SetProduct(ctx, boss, "spot", false, "cancel the rest")
		if err != nil || ps.Cancel == nil || *ps.Cancel != (ProductCancel{Status: CancelFailed, Reason: tc.reason}) {
			t.Fatalf("%s: %+v %v", tc.reason, ps.Cancel, err)
		}
		if got := h.auditsOf("admin.products.orders_canceled"); !strings.Contains(got[len(got)-1], `"cancel_reason":"`+tc.reason+`"`) ||
			strings.Contains(got[len(got)-1], "spot-trading-service") || strings.Contains(got[len(got)-1], "cancel_error") {
			t.Fatalf("%s audited %v", tc.reason, got)
		}
	}
	// A cancel failing part way says what it canceled and for how many users
	// it could not; a closed line whose count is unknown is asked again all
	// the same.
	svcs.cancel, svcs.err = ports.ProductCanceled{Orders: 3, FailedUsers: 2}, apperr.Unavailable(errors.New("a user's lock"))
	svcs.errs = map[string]error{"spot": errors.New("trading slow")}
	ps, err = h.svc.SetProduct(ctx, boss, "spot", false, "cancel the rest")
	if err != nil || ps.Cancel == nil || *ps.Cancel != (ProductCancel{
		Status: CancelFailed, Canceled: 3, FailedUsers: 2, Reason: CancelPartial, Error: "COMMON_UNAVAILABLE: service temporarily unavailable",
	}) || ps.Lines[0].OpenOrders != nil {
		t.Fatalf("a cancel failing part way %+v %v", ps.Cancel, err)
	}
	if got := h.auditsOf("admin.products.orders_canceled"); len(got) != 4 || !strings.Contains(got[3], `"canceled_orders":3`) ||
		!strings.Contains(got[3], `"cancel":"failed"`) || !strings.Contains(got[3], `"failed_users":2`) || !strings.Contains(got[3], `"cancel_reason":"PARTIAL"`) {
		t.Fatalf("audited %v", got)
	}
	svcs.cancel, svcs.err, svcs.errs = ports.ProductCanceled{}, nil, nil

	// The caller gone right after the switch (A91): the flag changed, so the
	// switch is audited all the same (the store fails on a context done, as
	// a database does).
	gone, leave := context.WithCancel(ctx)
	pf.afterSwitch = leave
	_, _ = h.svc.SetProduct(gone, boss, "usdt_m", false, "close usdt_m, the caller gone")
	pf.afterSwitch = nil
	if got := h.auditsOf("admin.products.toggled"); pf.flags["product.usdt_m"].Enabled ||
		!strings.HasPrefix(got[len(got)-1], "product:usdt_m close usdt_m, the caller gone ") {
		t.Fatalf("switched with the caller gone %v", got)
	}
	if _, err := h.svc.SetProduct(ctx, boss, "usdt_m", true, "open usdt_m again"); err != nil {
		t.Fatal(err)
	}

	// A line whose service cannot count is null and named partial, the
	// others as counted.
	svcs.errs = map[string]error{"usdt_m": ports.ErrProductLineMissing}
	if ps, err := h.svc.Products(ctx, aud); err != nil || len(ps.Partial) != 1 || ps.Partial[0] != "usdt_m" || ps.Lines[1].OpenOrders != nil ||
		ps.Lines[1].OpenPositions != nil || ps.Lines[2].OpenOrders == nil {
		t.Fatalf("counts unknown %+v %v", ps, err)
	}
	svcs.errs = nil

	// The flags page leaves the lines to their card.
	if _, err := h.svc.SwitchFlag(ctx, boss, "product.coin_m", false, "around the card"); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("through the flags page: %v", err)
	}

	// The checklist: for information, what is open.
	c, err := h.svc.LaunchChecklist(ctx, aud, "admin.astras.vip")
	if err != nil {
		t.Fatal(err)
	}
	it := c.Items[slicesIndex(c, "products")]
	raw, _ := json.Marshal(it.Value)
	if it.Status != LaunchOK || !strings.Contains(string(raw), `"spot":{"closed_at":"`) || !strings.Contains(string(raw), `"coin_m":{"closed_at":null,"enabled":true}`) {
		t.Fatalf("the checklist's products %+v %s", it, raw)
	}
	pf.down = true
	if c, _ := h.svc.LaunchChecklist(ctx, aud, "admin.astras.vip"); launchStatuses(c)["products"] != LaunchUnknown {
		t.Fatalf("flags unknown %v", launchStatuses(c))
	}
}
