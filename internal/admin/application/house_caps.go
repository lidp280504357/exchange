package application

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// HOUSE's caps at run time (user 2026-10-07 04:4x, A69): what market-maker
// quotes within (review C45) - a level, a pair's and all spot positions, a
// contract's position (USDT), the backed inventory kept back (USDT) and
// the contracts' leverage on HOUSE's contract equity. Every administrator
// reads them, beside the ones market-maker started with; a change is asked
// by whoever may ask for HOUSE's fund operations (ledger.adjust.request),
// each cap within its range (user 06:0x, review C47), and always waits for
// a second administrator (HOUSE_CAPS, ledger.adjust.approve), whatever the
// approval mode; approved, market-maker takes it as of the version read.

// houseCapsFields are the caps by their names in market-maker's API, in
// the order the console shows them.
var houseCapsFields = []string{"level", "symbol", "total", "contract", "safety", "contract_leverage"}

// houseCapsLeverage is the one cap that is a multiple, from 1 to 125
// (the contracts' own highest leverage); the others are USDT.
const houseCapsLeverage = "contract_leverage"

// houseCapsLevel is the one USDT cap that may be zero: a level not capped.
const houseCapsLevel = "level"

var (
	// houseCapsMax bounds the USDT caps.
	houseCapsMax = decimal.New(1, 15)
	// houseCapsLeverageMax is the contracts' highest leverage.
	houseCapsLeverageMax = decimal.NewFromInt(125)
)

// checkHouseCap refuses a cap out of its range: the level cap zero or
// more (zero: a level is not capped), the other USDT caps above zero, all
// of them at most 1e15 USDT; the leverage from 1 to 125.
func checkHouseCap(name string, d decimal.Decimal) error {
	switch {
	case name == houseCapsLeverage:
		if d.LessThan(decimal.NewFromInt(1)) || d.GreaterThan(houseCapsLeverageMax) {
			return apperr.Invalid(name + " must be from 1 to 125")
		}
	case d.GreaterThan(houseCapsMax):
		return apperr.Invalid(name + " must be at most 1000000000000000 USDT")
	case name == houseCapsLevel && d.IsNegative():
		return apperr.Invalid(name + " must be zero or more (zero: a level is not capped)")
	case name != houseCapsLevel && !d.IsPositive():
		return apperr.Invalid(name + " must be above zero")
	}
	return nil
}

// houseCapsApprovalTTL: a caps request is decided within a day, while the
// caps it was asked against still hold.
const houseCapsApprovalTTL = 24 * time.Hour

// houseCapsChangesShown bounds the latest changes the console shows;
// houseCapsChangesRead are read, to find the first version among them.
const (
	houseCapsChangesShown = 10
	houseCapsChangesRead  = 100
)

// ErrHouseCapsPending refuses a second caps request while one waits.
var ErrHouseCapsPending = apperr.New(apperr.KindConflict, "ADMIN_HOUSE_CAPS_PENDING",
	"a request for HOUSE's caps waits already: decide or reject that one first")

// errHouseCapsVersion is market-maker's answer to caps that moved since
// they were read, given before asking it.
var errHouseCapsVersion = apperr.New(apperr.KindConflict, "HOUSE_CAPS_VERSION", "HOUSE's caps changed since they were read")

// HouseCaps are HOUSE's caps as market-maker keeps them: decimal strings,
// USDT but the leverage.
type HouseCaps struct {
	Level            string `json:"level"`
	Symbol           string `json:"symbol"`
	Total            string `json:"total"`
	Contract         string `json:"contract"`
	Safety           string `json:"safety"`
	ContractLeverage string `json:"contract_leverage"`
	Version          int64  `json:"version"`
	UpdatedBy        string `json:"updated_by"`
	UpdatedAt        string `json:"updated_at"`
}

// value is a cap by its name.
func (c HouseCaps) value(name string) string {
	return map[string]string{
		"level": c.Level, "symbol": c.Symbol, "total": c.Total, "contract": c.Contract, "safety": c.Safety,
		houseCapsLeverage: c.ContractLeverage,
	}[name]
}

// HouseCapsChange is one change of the caps, as market-maker keeps it,
// with the key that signed it (review C47: "admin" for the console's,
// "ops" for exchangectl's, empty before C47 and for the first version).
type HouseCapsChange struct {
	Version    int64           `json:"version"`
	Caps       json.RawMessage `json:"caps"`
	Previous   json.RawMessage `json:"previous"`
	Actor      string          `json:"actor"`
	Approver   string          `json:"approver"`
	ApprovalID string          `json:"approval_id"`
	Reason     string          `json:"reason"`
	SignedBy   string          `json:"signed_by"`
	At         string          `json:"at"`
}

// HouseCapsView is the caps with the request that waits to change them
// (nil for none), the latest changes, newest first, and the caps of the
// first version - the deployment's, from market-maker's environment (nil
// when it is not among the latest changes read).
type HouseCapsView struct {
	Caps    HouseCaps
	Pending *domain.Approval
	Changes []HouseCapsChange
	Initial json.RawMessage
}

// HouseCapsRequest asks to change some caps (by their names) of the
// version read.
type HouseCapsRequest struct {
	Caps    map[string]string
	Version int64
	Reason  string
}

func (s *Service) houseCaps(ctx context.Context) (HouseCaps, error) {
	if s.MarketMaker == nil {
		return HouseCaps{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-maker is not configured")
	}
	raw, err := s.MarketMaker.HouseCaps(ctx)
	if err != nil {
		return HouseCaps{}, err
	}
	var c HouseCaps
	if err := json.Unmarshal(raw, &c); err != nil || c.Version < 1 {
		return HouseCaps{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-maker answered the caps in another shape")
	}
	return c, nil
}

// HouseCapsOf returns HOUSE's caps with the request that waits, the
// latest changes and the first version's caps (none when they cannot be
// read).
func (s *Service) HouseCapsOf(ctx context.Context, p Principal) (HouseCapsView, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return HouseCapsView{}, err
	}
	caps, err := s.houseCaps(ctx)
	if err != nil {
		return HouseCapsView{}, err
	}
	out := HouseCapsView{Caps: caps, Changes: []HouseCapsChange{}}
	pending, err := s.Store.Read().Approvals().PendingOfKind(ctx, domain.KindHouseCaps)
	if err != nil {
		return HouseCapsView{}, err
	}
	now := s.Now()
	for i := range pending {
		if !lapsedAt(pending[i], now) {
			out.Pending = &pending[i]
			break
		}
	}
	if raw, err := s.MarketMaker.HouseCapsChanges(ctx, houseCapsChangesRead); err != nil {
		s.Log.WarnContext(ctx, "house caps: the changes cannot be read", "error", err)
	} else {
		var list struct {
			Items []HouseCapsChange `json:"items"`
		}
		if json.Unmarshal(raw, &list) == nil && list.Items != nil {
			out.Changes = list.Items[:min(len(list.Items), houseCapsChangesShown)]
			for _, c := range list.Items {
				if c.Version == 1 {
					out.Initial = c.Caps
				}
			}
		}
	}
	return out, nil
}

// RequestHouseCaps asks a second administrator to change HOUSE's caps:
// those given that differ from the caps of the version read, each within
// its range (checkHouseCap). One request waits at a time.
func (s *Service) RequestHouseCaps(ctx context.Context, p Principal, in HouseCapsRequest) (domain.Approval, error) {
	if err := p.require(domain.PermAdjustRequest); err != nil {
		return domain.Approval{}, err
	}
	if err := needReason(in.Reason); err != nil {
		return domain.Approval{}, err
	}
	for name := range in.Caps {
		if !slices.Contains(houseCapsFields, name) {
			return domain.Approval{}, apperr.Invalid("no cap " + name + " (level, symbol, total, contract, safety, contract_leverage)")
		}
	}
	cur, err := s.houseCaps(ctx)
	if err != nil {
		return domain.Approval{}, err
	}
	if in.Version != cur.Version {
		return domain.Approval{}, errHouseCapsVersion.WithDetail("version", cur.Version)
	}
	asked, previous := map[string]string{}, map[string]string{}
	var changed []string
	for _, name := range houseCapsFields {
		v, ok := in.Caps[name]
		if !ok {
			continue
		}
		d, err := decimal.NewFromString(strings.TrimSpace(v))
		if err != nil {
			return domain.Approval{}, apperr.Invalid(name + " must be a decimal")
		}
		if err := checkHouseCap(name, d); err != nil {
			return domain.Approval{}, err
		}
		if was, err := decimal.NewFromString(cur.value(name)); err == nil && was.Equal(d) {
			continue
		}
		asked[name], previous[name] = d.String(), cur.value(name)
		changed = append(changed, name)
	}
	if len(changed) == 0 {
		return domain.Approval{}, apperr.Invalid("nothing changes: every cap given is as it is")
	}
	caps, _ := json.Marshal(asked)
	before, _ := json.Marshal(previous)
	a := domain.Approval{
		ID: uuid.Must(uuid.NewV7()).String(), Kind: domain.KindHouseCaps, Reason: strings.TrimSpace(in.Reason), Status: domain.ApprovalPending,
		Payload: map[string]string{
			"caps": string(caps), "previous": string(before), "expected_version": strconv.FormatInt(cur.Version, 10),
			"changed": strings.Join(changed, ","), "actor": p.Admin.Email,
		},
		RequestedBy: p.Admin.ID, RequestedByEmail: p.Admin.Email, CreatedAt: s.Now(), Mode: domain.ModeTwoPerson,
	}
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Approvals().LockRequests(ctx, a.Kind, houseCapsTarget); err != nil {
			return err
		}
		pending, err := r.Approvals().PendingOfKind(ctx, a.Kind)
		if err != nil {
			return err
		}
		for _, o := range pending {
			if !lapsedAt(o, a.CreatedAt) {
				return ErrHouseCapsPending.WithDetail("approval_id", o.ID)
			}
		}
		if err := r.Approvals().Insert(ctx, a); err != nil {
			return err
		}
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: houseCapsTarget, Action: fundActions[a.Kind].requested, Actor: p.Admin.Email, Reason: a.Reason, Details: fundDetails(a),
		}, p.Admin.Email)
	})
	if err != nil {
		return domain.Approval{}, err
	}
	return a, nil
}

// houseCapsTarget is the audit target of HOUSE's caps.
const houseCapsTarget = "house:caps"

// executeHouseCaps sets the caps an approved HOUSE_CAPS asks for, as of the
// version it was asked against, in its requester's name with its approver,
// and audits each cap's change (admin.house.caps_changed). The caps moved
// since then refuse it (HOUSE_CAPS_VERSION) and the request fails, unless
// that move is this request's own earlier attempt whose answer was lost.
func (s *Service) executeHouseCaps(ctx context.Context, a domain.Approval, p Principal) (string, error) {
	if s.MarketMaker == nil {
		return "", apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "market-maker is not configured")
	}
	version, err := strconv.ParseInt(a.Payload["expected_version"], 10, 64)
	if err != nil {
		return "", err
	}
	var caps map[string]string
	if err := json.Unmarshal([]byte(a.Payload["caps"]), &caps); err != nil {
		return "", err
	}
	out, err := s.MarketMaker.SetHouseCaps(ctx, ports.HouseCapsWrite{
		Caps: caps, Version: version, Actor: a.Payload["actor"], Approver: p.Admin.Email, ApprovalID: a.ID, Reason: a.Reason,
	})
	if apperr.Is(err, errHouseCapsVersion.Code) {
		// An earlier attempt whose answer was lost may have set them: a
		// change of this request's in market-maker's history is that
		// attempt. When it cannot be read the outcome is unknown and the
		// request stays pending.
		done, rerr := s.houseCapsSetBy(ctx, a.ID)
		if rerr != nil {
			return "", apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "HOUSE's caps changed and their history cannot be read: try again")
		}
		if done > 0 {
			return fmt.Sprintf("caps version %d (set by an earlier attempt)", done), nil
		}
	}
	if err != nil {
		return "", err
	}
	var set HouseCaps
	_ = json.Unmarshal(out, &set)
	var previous map[string]string
	_ = json.Unmarshal([]byte(a.Payload["previous"]), &previous)
	changes := make([]map[string]string, 0, len(caps))
	for _, name := range strings.Split(a.Payload["changed"], ",") {
		changes = append(changes, map[string]string{"cap": name, "before": previous[name], "after": caps[name]})
	}
	details, _ := json.Marshal(map[string]any{
		"approval_id": a.ID, "requested_by": a.RequestedByEmail, "approved_by": p.Admin.Email, "changes": changes, "version": set.Version,
	})
	if err := s.audit(ctx, p, houseCapsTarget, "admin.house.caps_changed", a.Reason, string(details)); err != nil {
		// Changed, its audit event lost: the approval's own (approved) says so.
		s.Log.ErrorContext(ctx, "house caps: changed, unaudited", "approval_id", a.ID, "error", err)
	}
	return fmt.Sprintf("caps version %d", set.Version), nil
}

// houseCapsSetBy is the version market-maker's history has of a change by
// approvalID (0 for none among the latest).
func (s *Service) houseCapsSetBy(ctx context.Context, approvalID string) (int64, error) {
	raw, err := s.MarketMaker.HouseCapsChanges(ctx, 100)
	if err != nil {
		return 0, err
	}
	var list struct {
		Items []HouseCapsChange `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return 0, err
	}
	for _, c := range list.Items {
		if c.ApprovalID == approvalID {
			return c.Version, nil
		}
	}
	return 0, nil
}
