package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	auditv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/audit/v1"
	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/pagecursor"
)

// Trading parameters (design 2026-10-02 §2 item 6): one call must not be
// able to set a fee of 10%, liquidate the high-leverage positions within
// seconds or cut HOUSE's liquidity. A pair's or contract's status, fee
// rates, risk ladders (and so leverage) and reference symbols change only
// by an ADMIN (instruments.trading), who confirms what the server's
// preview showed (a sealed token bound to them and to exactly that
// change, valid 10 minutes); the change then waits the settings' delay,
// and while two-person approval is on it first waits for a second ADMIN.
// Any ADMIN may cancel it until it takes effect. A halt is the emergency
// brake: it takes effect at once. New pairs and contracts start in
// PREPARE and touch nobody until opened, which is a status change.

// tradingParams are the fields of each entity that are trading
// parameters.
var tradingParams = map[string][]string{
	"FEE_SCHEDULE": {"maker_fee_rate", "taker_fee_rate"},
	"TRADING_PAIR": {"fee_tier", "reference_symbol", "reference_multiplier"},
	"CONTRACT":     {"fee_tier", "risk_tiers"},
}

// confirmationTTL bounds how long a preview's confirmation holds.
const confirmationTTL = 10 * time.Minute

// ParamChange is a trading parameter a change moves.
type ParamChange struct {
	Entity string          `json:"entity"`
	Key    string          `json:"key"`
	Field  string          `json:"field"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// Confirmation binds the call that confirms a change to the preview an
// ADMIN saw.
type Confirmation struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Guard is what a preview says of a change's trading parameters.
type Guard struct {
	// Params are the trading parameters it moves; without any the change
	// takes effect at once.
	Params []ParamChange `json:"params"`
	// Impacts are what new risk ladders would do to the open positions.
	Impacts []ports.TierImpact `json:"impacts"`
	// Confirmation is what the confirming call brings back; nil without
	// trading parameters, for a caller who may not change them, or while
	// a ladder's impact cannot be measured.
	Confirmation *Confirmation `json:"confirmation"`
	DelaySeconds int           `json:"delay_seconds"`
	TwoPerson    bool          `json:"two_person"`
}

// ConfigPreview is what a config document would do.
type ConfigPreview struct {
	ports.ConfigResult
	Guard Guard `json:"guard"`
	// fingerprint identifies the changes previewed.
	fingerprint string
}

// The preview's notes on trading parameters.
const (
	// WarnImpactUnknown: derivatives-service could not measure a new
	// ladder, so the change cannot be confirmed now.
	WarnImpactUnknown = "IMPACT_UNKNOWN"
)

// ErrReferenceInUse refuses clearing the reference symbol of a pair HOUSE
// quotes or a perpetual's index follows: its liquidity or its index would
// stop.
var ErrReferenceInUse = apperr.New(apperr.KindUnprocessable, "ADMIN_REFERENCE_IN_USE",
	"HOUSE quotes this pair or a perpetual's index follows it; its reference symbol stays")

// paramChanges lists the trading parameters the updates among changes
// move.
func paramChanges(changes []ports.ConfigChange) []ParamChange {
	out := []ParamChange{}
	for _, c := range changes {
		fields := tradingParams[c.Entity]
		if c.Action != "UPDATE" || len(fields) == 0 {
			continue
		}
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal(c.Before, &before)
		_ = json.Unmarshal(c.After, &after)
		for _, f := range fields {
			b, a := orNull(before[f]), orNull(after[f])
			if !bytes.Equal(b, a) {
				out = append(out, ParamChange{Entity: c.Entity, Key: c.Key, Field: f, Before: b, After: a})
			}
		}
	}
	return out
}

func orNull(v json.RawMessage) json.RawMessage {
	if len(v) == 0 || bytes.Equal(v, []byte(`""`)) {
		return json.RawMessage("null")
	}
	return v
}

// fingerprintOf identifies changes: a later dry run that differs in any
// item, field or version is not what was confirmed.
func fingerprintOf(changes []ports.ConfigChange) string {
	raw, _ := json.Marshal(changes)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func statusFingerprint(kind, symbol, from, to string) string {
	return strings.Join([]string{kind, symbol, from, to}, "|")
}

type confirmClaims struct {
	Kind        string `json:"k"`
	Fingerprint string `json:"f"`
	Expires     int64  `json:"e"`
}

func confirmAAD(p Principal) []byte { return []byte("instrument-change:" + p.Admin.ID) }

// confirmation seals what p previewed for the confirming call.
func (s *Service) confirmation(p Principal, kind, fingerprint string) *Confirmation {
	exp := s.Now().Add(confirmationTTL).Truncate(time.Second)
	raw, _ := json.Marshal(confirmClaims{Kind: kind, Fingerprint: fingerprint, Expires: exp.Unix()})
	return &Confirmation{Token: base64.RawURLEncoding.EncodeToString(s.Box.Seal(raw, confirmAAD(p))), ExpiresAt: exp.UTC()}
}

// confirmed checks that token is p's confirmation of exactly this change.
func (s *Service) confirmed(p Principal, token, kind, fingerprint string) error {
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if token == "" || err != nil {
		return domain.ErrConfirmationRequired
	}
	raw, err := s.Box.Open(sealed, confirmAAD(p))
	if err != nil {
		return domain.ErrConfirmationRequired
	}
	var c confirmClaims
	switch {
	case json.Unmarshal(raw, &c) != nil || c.Kind != kind:
		return domain.ErrConfirmationRequired
	case s.Now().Unix() > c.Expires:
		return domain.ErrConfirmationRequired.WithDetail("reason", "expired")
	case c.Fingerprint != fingerprint:
		return domain.ErrConfirmationRequired.WithDetail("reason", "changed")
	}
	return nil
}

// changeDelay is the settings' delay of trading parameters' changes.
func (s *Service) changeDelay(ctx context.Context) (time.Duration, error) {
	set, err := s.settings(ctx, s.Store.Read())
	if err != nil {
		return 0, err
	}
	return set.ChangeDelay, nil
}

// previewConfig works out what a document changes and guards its trading
// parameters.
func (s *Service) previewConfig(ctx context.Context, p Principal, config json.RawMessage) (ConfigPreview, error) {
	res, err := s.applyConfig(ctx, p, config, "preview", true)
	if err != nil {
		return ConfigPreview{}, err
	}
	out := ConfigPreview{
		ConfigResult: res, Guard: Guard{Params: paramChanges(res.Changes), Impacts: []ports.TierImpact{}},
		fingerprint: fingerprintOf(res.Changes),
	}
	if len(out.Guard.Params) == 0 {
		return out, nil
	}
	delay, err := s.changeDelay(ctx)
	if err != nil {
		return ConfigPreview{}, err
	}
	out.Guard.DelaySeconds, out.Guard.TwoPerson = int(delay/time.Second), s.TwoPerson()
	measured := true
	for _, pc := range out.Guard.Params {
		if pc.Entity != "CONTRACT" || pc.Field != "risk_tiers" {
			continue
		}
		var imp ports.TierImpact
		if s.Derivatives != nil {
			imp, err = s.Derivatives.TierImpact(ctx, pc.Key, pc.After)
		}
		if s.Derivatives == nil || err != nil {
			s.Log.WarnContext(ctx, "instruments: tier impact unavailable", "symbol", pc.Key, "error", err)
			out.Warnings = append(out.Warnings, ports.ConfigWarning{Code: WarnImpactUnknown, Symbol: pc.Key})
			measured = false
			continue
		}
		out.Guard.Impacts = append(out.Guard.Impacts, imp)
	}
	if measured && p.require(domain.PermInstrumentsTrading) == nil {
		out.Guard.Confirmation = s.confirmation(p, domain.ChangeConfig, out.fingerprint)
	}
	return out, nil
}

// changeSummary is what a change's confirmation showed, kept with it.
type changeSummary struct {
	Fingerprint string `json:"fingerprint"`
	// From is a status change's status when confirmed.
	From    string             `json:"from,omitempty"`
	Params  []ParamChange      `json:"params"`
	Impacts []ports.TierImpact `json:"impacts"`
	Items   []changeItem       `json:"items"`
}

type changeItem struct {
	Entity  string `json:"entity"`
	Key     string `json:"key"`
	Action  string `json:"action"`
	Version int64  `json:"version"`
}

func items(changes []ports.ConfigChange) []changeItem {
	out := make([]changeItem, 0, len(changes))
	for _, c := range changes {
		out = append(out, changeItem{Entity: c.Entity, Key: c.Key, Action: c.Action, Version: c.Version})
	}
	return out
}

// request records a confirmed change: waiting for a second ADMIN or
// scheduled, audited as admin.instruments.change_requested.
func (s *Service) request(ctx context.Context, p Principal, kind, target string, payload json.RawMessage, summary changeSummary,
	reason string,
) (domain.InstrumentChange, error) {
	delay, err := s.changeDelay(ctx)
	if err != nil {
		return domain.InstrumentChange{}, err
	}
	sum, _ := json.Marshal(summary)
	c := domain.NewInstrumentChange(uuid.Must(uuid.NewV7()).String(), kind, target, payload, sum, reason, p.Admin.ID, s.TwoPerson(),
		delay, s.Now())
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		if err := r.Changes().Create(ctx, c); err != nil {
			return err
		}
		details, _ := json.Marshal(map[string]any{
			"change_id": c.ID, "kind": kind, "status": c.Status, "params": summary.Params,
			"effective_at": stampOrNil(c.EffectiveAt),
		})
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: target, Action: "admin.instruments.change_requested", Actor: p.Admin.Email, Reason: reason, Details: string(details),
		}, p.Admin.Email)
	})
	c.RequestedByEmail = p.Admin.Email
	return c, err
}

func stampOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// pairTransitions is the pair and contract state machine (appendix B).
var pairTransitions = map[string][]string{
	"PREPARE": {"TRADING"}, "TRADING": {"HALT", "CANCEL_ONLY"}, "HALT": {"TRADING", "CANCEL_ONLY"}, "CANCEL_ONLY": {"DELISTED"},
}

const statusHalt = "HALT"

// StatusPreview is what moving a pair or contract to another status does.
type StatusPreview struct {
	Symbol string `json:"symbol"`
	From   string `json:"from"`
	To     string `json:"to"`
	// Immediate: a halt takes effect at once, without confirmation.
	Immediate    bool          `json:"immediate"`
	Confirmation *Confirmation `json:"confirmation"`
	DelaySeconds int           `json:"delay_seconds"`
	TwoPerson    bool          `json:"two_person"`
}

// StatusResult is a status change done, or the change waiting.
type StatusResult struct {
	From   string
	To     string
	Change *domain.InstrumentChange
}

// currentStatus reads a pair's or contract's status from the reference
// data.
func (s *Service) currentStatus(ctx context.Context, kind, symbol string) (string, error) {
	raw, err := s.Catalog.Export(ctx)
	if err != nil {
		return "", err
	}
	var doc struct {
		Pairs []struct {
			Symbol string `json:"symbol"`
			Status string `json:"status"`
		} `json:"pairs"`
		Contracts []struct {
			Symbol string `json:"symbol"`
			Status string `json:"status"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "instrument-service answered badly")
	}
	list := doc.Pairs
	if kind == domain.ChangeContractStatus {
		list = doc.Contracts
	}
	for _, x := range list {
		if x.Symbol == symbol {
			return x.Status, nil
		}
	}
	return "", apperr.NotFound("no such pair or contract: " + symbol)
}

// PreviewStatus shows what moving a pair (kind PAIR_STATUS) or contract
// (CONTRACT_STATUS) to another status does, with the confirmation its
// change needs.
func (s *Service) PreviewStatus(ctx context.Context, p Principal, kind, symbol, to string) (StatusPreview, error) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return StatusPreview{}, err
	}
	symbol, to = strings.ToUpper(strings.TrimSpace(symbol)), strings.ToUpper(strings.TrimSpace(to))
	from, err := s.currentStatus(ctx, kind, symbol)
	if err != nil {
		return StatusPreview{}, err
	}
	if !contains(pairTransitions[from], to) {
		return StatusPreview{}, apperr.New(apperr.KindConflict, "INSTRUMENT_STATUS_TRANSITION_INVALID",
			fmt.Sprintf("%s cannot move from %s to %s", symbol, from, to))
	}
	out := StatusPreview{Symbol: symbol, From: from, To: to, Immediate: to == statusHalt}
	if out.Immediate {
		return out, nil
	}
	delay, err := s.changeDelay(ctx)
	if err != nil {
		return StatusPreview{}, err
	}
	out.DelaySeconds, out.TwoPerson = int(delay/time.Second), s.TwoPerson()
	out.Confirmation = s.confirmation(p, kind, statusFingerprint(kind, symbol, from, to))
	return out, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// setStatus moves a pair or contract: a halt at once, anything else as a
// confirmed change.
func (s *Service) setStatus(ctx context.Context, p Principal, kind, symbol, to, reason, token string) (StatusResult, error) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return StatusResult{}, err
	}
	if err := needReason(reason); err != nil {
		return StatusResult{}, err
	}
	reason = strings.TrimSpace(reason)
	prev, err := s.PreviewStatus(ctx, p, kind, symbol, to)
	if err != nil {
		return StatusResult{}, err
	}
	symbol, to = prev.Symbol, prev.To
	if prev.Immediate {
		from, err := s.moveStatus(ctx, kind, symbol, to, reason, p.Admin.Email)
		if err != nil {
			return StatusResult{}, err
		}
		target, action := statusAudit(kind, symbol)
		return StatusResult{From: from, To: to}, s.audit(ctx, p, target, action, reason, fmt.Sprintf(`{"from":%q,"to":%q}`, from, to))
	}
	if err := s.confirmed(p, token, kind, statusFingerprint(kind, symbol, prev.From, to)); err != nil {
		return StatusResult{}, err
	}
	payload, _ := json.Marshal(map[string]string{"symbol": symbol, "from": prev.From, "to": to})
	target, _ := statusAudit(kind, symbol)
	c, err := s.request(ctx, p, kind, target, payload, changeSummary{
		Fingerprint: statusFingerprint(kind, symbol, prev.From, to), From: prev.From, Params: []ParamChange{{
			Entity: map[string]string{domain.ChangePairStatus: "TRADING_PAIR", domain.ChangeContractStatus: "CONTRACT"}[kind], Key: symbol,
			Field: "status", Before: json.RawMessage(fmt.Sprintf("%q", prev.From)), After: json.RawMessage(fmt.Sprintf("%q", to)),
		}}, Impacts: []ports.TierImpact{}, Items: []changeItem{},
	}, reason)
	if err != nil {
		return StatusResult{}, err
	}
	return StatusResult{From: prev.From, To: to, Change: &c}, nil
}

func statusAudit(kind, symbol string) (target, action string) {
	if kind == domain.ChangeContractStatus {
		return "contract:" + symbol, "admin.instruments.contract_status"
	}
	return "pair:" + symbol, "admin.instruments.pair_status"
}

func (s *Service) moveStatus(ctx context.Context, kind, symbol, to, reason, actor string) (string, error) {
	if kind == domain.ChangeContractStatus {
		return s.Catalog.SetContractStatus(ctx, symbol, to, reason, actor)
	}
	return s.Catalog.SetPairStatus(ctx, symbol, to, reason, actor)
}

// SetPairStatus moves a trading pair to another status: a halt at once,
// anything else once confirmed, after the delay.
func (s *Service) SetPairStatus(ctx context.Context, p Principal, symbol, to, reason, token string) (StatusResult, error) {
	return s.setStatus(ctx, p, domain.ChangePairStatus, symbol, to, reason, token)
}

// SetContractStatus moves a perpetual contract to another status, as
// SetPairStatus a pair; instrument-service records the change.
func (s *Service) SetContractStatus(ctx context.Context, p Principal, symbol, to, reason, token string) (StatusResult, error) {
	return s.setStatus(ctx, p, domain.ChangeContractStatus, symbol, to, reason, token)
}

// InstrumentChanges returns a page of the changes of trading parameters,
// newest first, of a status when set; every administrator reads them.
func (s *Service) InstrumentChanges(ctx context.Context, p Principal, status, cursor string, limit int) ([]domain.InstrumentChange, string, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return nil, "", err
	}
	afterTime, afterID, err := pagecursor.Decode(cursor)
	if err != nil {
		return nil, "", apperr.Invalid("bad cursor")
	}
	limit = pageLimit(limit)
	list, err := s.Store.Read().Changes().List(ctx, strings.ToUpper(status), afterTime, afterID, limit+1)
	if err != nil || len(list) <= limit {
		return list, "", err
	}
	list = list[:limit]
	last := list[limit-1]
	return list, pagecursor.Encode(last.CreatedAt, last.ID), nil
}

// DecideInstrumentChange approves (schedules after the delay) or rejects a
// change waiting for a second ADMIN.
func (s *Service) DecideInstrumentChange(ctx context.Context, p Principal, id string, approve bool, reason string) (domain.InstrumentChange, error) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return domain.InstrumentChange{}, err
	}
	if err := needReason(reason); err != nil {
		return domain.InstrumentChange{}, err
	}
	delay, err := s.changeDelay(ctx)
	if err != nil {
		return domain.InstrumentChange{}, err
	}
	action := "admin.instruments.change_rejected"
	if approve {
		action = "admin.instruments.change_approved"
	}
	return s.changeOne(ctx, p, id, reason, action, func(c *domain.InstrumentChange) error {
		if approve {
			return c.Approve(p.Admin.ID, delay, s.Now())
		}
		return c.Reject(p.Admin.ID, s.Now())
	})
}

// CancelInstrumentChange withdraws a change before it takes effect.
func (s *Service) CancelInstrumentChange(ctx context.Context, p Principal, id, reason string) (domain.InstrumentChange, error) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return domain.InstrumentChange{}, err
	}
	if err := needReason(reason); err != nil {
		return domain.InstrumentChange{}, err
	}
	return s.changeOne(ctx, p, id, reason, "admin.instruments.change_canceled", func(c *domain.InstrumentChange) error {
		return c.Cancel(p.Admin.ID, s.Now())
	})
}

func (s *Service) changeOne(ctx context.Context, p Principal, id, reason, action string, fn func(*domain.InstrumentChange) error) (domain.InstrumentChange, error) {
	var out domain.InstrumentChange
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		c, err := r.Changes().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if c == nil {
			return apperr.NotFound("no such change")
		}
		if err := fn(c); err != nil {
			return err
		}
		if err := r.Changes().Update(ctx, *c); err != nil {
			return err
		}
		out = *c
		details, _ := json.Marshal(map[string]any{"change_id": c.ID, "status": c.Status, "effective_at": stampOrNil(c.EffectiveAt)})
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: c.Target, Action: action, Actor: p.Admin.Email, Reason: strings.TrimSpace(reason), Details: string(details),
		}, p.Admin.Email)
	})
	if err != nil {
		return domain.InstrumentChange{}, err
	}
	if fresh, err := s.Store.Read().Changes().Get(ctx, out.ID); err == nil && fresh != nil {
		out = *fresh
	}
	return out, nil
}

// dueBatch bounds the changes applied in one round.
const dueBatch = 5

// ApplyDueChanges applies the scheduled changes whose time has come
// (admin-service runs it every few seconds) and returns how many it
// settled. A change that is no longer what was confirmed fails; one that
// cannot be applied for a moment (a service down) waits for the next
// round.
func (s *Service) ApplyDueChanges(ctx context.Context) (int, error) {
	n := 0
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		due, err := r.Changes().Due(ctx, s.Now(), dueBatch)
		if err != nil {
			return err
		}
		for _, c := range due {
			result, err := s.applyChange(ctx, c)
			if err != nil && apperr.From(err).Kind == apperr.KindUnavailable {
				s.Log.WarnContext(ctx, "instruments: change not applied yet", "change_id", c.ID, "error", err)
				continue
			}
			applied := err == nil
			if err != nil {
				result = err.Error()
			}
			c.Settle(applied, result, s.Now())
			if err := r.Changes().Update(ctx, c); err != nil {
				return err
			}
			action := "admin.instruments.change_applied"
			if !applied {
				action = "admin.instruments.change_failed"
			}
			details, _ := json.Marshal(map[string]any{"change_id": c.ID, "kind": c.Kind, "result": c.Result, "approved_by": c.ApprovedByEmail})
			if err := r.Audit(ctx, &auditv1.AdminActionPerformed{
				Target: c.Target, Action: action, Actor: c.RequestedByEmail, Reason: c.Reason, Details: string(details),
			}, c.RequestedByEmail); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// errNotConfirmed fails a change whose target moved since it was
// confirmed.
var errNotConfirmed = apperr.New(apperr.KindConflict, apperr.CodeConflict,
	"the reference data changed since the change was confirmed; preview it again")

// applyChange carries out a due change in the requester's name.
func (s *Service) applyChange(ctx context.Context, c domain.InstrumentChange) (string, error) {
	var sum changeSummary
	if err := json.Unmarshal(c.Summary, &sum); err != nil {
		return "", apperr.Invalid("the change's summary is unreadable")
	}
	switch c.Kind {
	case domain.ChangePairStatus, domain.ChangeContractStatus:
		var pl struct{ Symbol, From, To string }
		if err := json.Unmarshal(c.Payload, &pl); err != nil {
			return "", apperr.Invalid("the change's payload is unreadable")
		}
		cur, err := s.currentStatus(ctx, c.Kind, pl.Symbol)
		if err != nil {
			return "", err
		}
		if cur != pl.From {
			return "", errNotConfirmed
		}
		if _, err := s.moveStatus(ctx, c.Kind, pl.Symbol, pl.To, c.Reason, c.RequestedByEmail); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: %s → %s", pl.Symbol, pl.From, pl.To), nil
	case domain.ChangeConfig:
		var doc referenced
		if err := json.Unmarshal(c.Payload, &doc); err != nil {
			return "", apperr.Invalid("the change's document is unreadable")
		}
		if _, err := s.checkReferences(ctx, doc); err != nil {
			return "", err
		}
		dry, err := s.Catalog.Apply(ctx, c.Payload, true, c.RequestedByEmail, c.Reason)
		if err != nil {
			return "", err
		}
		if fingerprintOf(dry.Changes) != sum.Fingerprint {
			return "", errNotConfirmed
		}
		res, err := s.Catalog.Apply(ctx, c.Payload, false, c.RequestedByEmail, c.Reason)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d changed, %d unchanged", len(res.Changes), res.Unchanged), nil
	}
	return "", errors.New("unknown change kind " + c.Kind)
}
