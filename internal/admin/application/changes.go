package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
	"github.com/skill/exchange/internal/platform/pagecursor"
	"github.com/skill/exchange/internal/platform/pg"
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
	// WarnImpactUnmeasured: some of the contract's positions could not be
	// measured (no fresh mark price, or too many cross accounts to measure
	// in time), so the change cannot be confirmed now (C5.5 ⑩).
	WarnImpactUnmeasured = "IMPACT_UNMEASURED"
)

// ErrReferenceInUse refuses clearing the reference symbol of a pair HOUSE
// quotes or a perpetual's index follows: its liquidity or its index would
// stop.
var ErrReferenceInUse = apperr.New(apperr.KindUnprocessable, "ADMIN_REFERENCE_IN_USE",
	"HOUSE quotes this pair or a perpetual's index follows it; its reference symbol stays")

// ErrNewItemNotPrepare refuses a pair or contract created already open
// (instrument-service takes a new item's status from the document): it
// starts in PREPARE and opens with a status change, which is guarded.
var ErrNewItemNotPrepare = apperr.New(apperr.KindUnprocessable, "ADMIN_NEW_ITEM_NOT_PREPARE",
	"a new pair or contract starts in PREPARE; open it with a status change")

// newItemsPrepare checks that the pairs and contracts among changes that
// are created start in PREPARE.
func newItemsPrepare(changes []ports.ConfigChange) error {
	for _, c := range changes {
		if c.Action != "CREATE" || (c.Entity != "TRADING_PAIR" && c.Entity != "CONTRACT") {
			continue
		}
		var after struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(c.After, &after)
		if after.Status != "" && after.Status != "PREPARE" {
			return ErrNewItemNotPrepare.WithDetail("symbol", c.Key).WithDetail("status", after.Status)
		}
	}
	return nil
}

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

// confirmationHash identifies a confirmation token: the change it
// confirmed keeps it, so the token confirms one change (C5.5 ⑩).
func confirmationHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// confirmedBefore is the change p's confirmation token confirmed already:
// brought again, even once the change took effect, it answers with that
// change as it stands (C5.5 ⑩, ⑲).
func (s *Service) confirmedBefore(ctx context.Context, p Principal, token string) (*domain.InstrumentChange, error) {
	if token == "" {
		return nil, nil
	}
	c, err := s.Store.Read().Changes().ByConfirmation(ctx, confirmationHash(token))
	if err != nil || c == nil || c.RequestedBy != p.Admin.ID {
		return nil, err
	}
	return c, nil
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

// changeDelay is the settings' delay of trading parameters' changes, at
// least the floor.
func (s *Service) changeDelay(ctx context.Context) (time.Duration, error) {
	set, err := s.settings(ctx, s.Store.Read())
	if err != nil {
		return 0, err
	}
	return max(set.ChangeDelay, s.delayFloor()), nil
}

// delayFloor is the least a change of trading parameters waits
// (ChangeDelayFloor, else domain.DefaultChangeDelayFloor).
func (s *Service) delayFloor() time.Duration {
	if s.ChangeDelayFloor > 0 {
		return max(s.ChangeDelayFloor, domain.MinChangeDelay)
	}
	return domain.DefaultChangeDelayFloor
}

// previewConfig works out what a document changes and guards its trading
// parameters.
func (s *Service) previewConfig(ctx context.Context, p Principal, config json.RawMessage) (ConfigPreview, error) {
	res, err := s.applyConfig(ctx, p, config, "preview", true)
	if err != nil {
		return ConfigPreview{}, err
	}
	if err := newItemsPrepare(res.Changes); err != nil {
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
	// The impacts are measured for an ADMIN who may confirm them, no one
	// else: each measures every cross account on the contract (C5.5 ⑩).
	if p.require(domain.PermInstrumentsTrading) != nil {
		return out, nil
	}
	measured := true
	for _, pc := range out.Guard.Params {
		if pc.Entity != "CONTRACT" || pc.Field != "risk_tiers" {
			continue
		}
		imp, err := s.tierImpact(ctx, pc)
		if err != nil {
			s.Log.WarnContext(ctx, "instruments: tier impact unavailable", "symbol", pc.Key, "error", err)
			out.Warnings = append(out.Warnings, ports.ConfigWarning{Code: WarnImpactUnknown, Symbol: pc.Key})
			measured = false
			continue
		}
		out.Guard.Impacts = append(out.Guard.Impacts, imp)
		if imp.Unmeasured > 0 {
			// A ladder measured against part of the positions is not
			// confirmed: the rest might be the ones it liquidates.
			out.Warnings = append(out.Warnings, ports.ConfigWarning{Code: WarnImpactUnmeasured, Symbol: pc.Key, Detail: strconv.Itoa(imp.Unmeasured)})
			measured = false
		}
	}
	if measured {
		out.Guard.Confirmation = s.confirmation(p, domain.ChangeConfig, out.fingerprint)
	}
	return out, nil
}

// tierImpact measures a ladder change against the contract's positions.
func (s *Service) tierImpact(ctx context.Context, pc ParamChange) (ports.TierImpact, error) {
	if s.Derivatives == nil {
		return ports.TierImpact{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "derivatives-service is not configured")
	}
	return s.Derivatives.TierImpact(ctx, pc.Key, pc.After)
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
// scheduled, audited as admin.instruments.change_requested. The
// confirmation (token) confirms one change: brought again (a retry), it
// answers with the change it confirmed (C5.5 ⑩).
func (s *Service) request(ctx context.Context, p Principal, kind, target string, payload json.RawMessage, summary changeSummary,
	reason, token string,
) (domain.InstrumentChange, error) {
	delay, err := s.changeDelay(ctx)
	if err != nil {
		return domain.InstrumentChange{}, err
	}
	sum, _ := json.Marshal(summary)
	c := domain.NewInstrumentChange(uuid.Must(uuid.NewV7()).String(), kind, target, payload, sum, reason, p.Admin.ID, s.TwoPerson(),
		delay, s.Now())
	c.ConfirmationHash = confirmationHash(token)
	var prev *domain.InstrumentChange
	err = s.Store.Tx(ctx, func(r ports.Repos) error {
		var err error
		if prev, err = r.Changes().ByConfirmation(ctx, c.ConfirmationHash); err != nil || prev != nil {
			return err
		}
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
	if _, dup := pg.UniqueViolation(err); dup {
		// The same confirmation, concurrently: the change it confirmed.
		prev, err = s.Store.Read().Changes().ByConfirmation(ctx, c.ConfirmationHash)
		if err == nil && prev == nil {
			err = domain.ErrConfirmationRequired
		}
	}
	if err != nil {
		return domain.InstrumentChange{}, err
	}
	if prev != nil {
		return *prev, nil
	}
	c.RequestedByEmail = p.Admin.Email
	return c, nil
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
	// OpenOrders counts the orders resting on it (the read model, seconds
	// behind; nil when it cannot be read): a halt leaves them on the book,
	// their owners may cancel them (C5.5 ⑩).
	OpenOrders *int `json:"open_orders"`
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
	if s.Records != nil {
		if n, err := s.Records.OpenOrders(ctx, symbol); err == nil {
			out.OpenOrders = &n
		} else {
			s.Log.WarnContext(ctx, "instruments: count the open orders", "symbol", symbol, "error", err)
		}
	}
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
	if done, err := s.confirmedBefore(ctx, p, token); err != nil || done != nil {
		if err != nil {
			return StatusResult{}, err
		}
		var pl struct{ From, To string }
		_ = json.Unmarshal(done.Payload, &pl)
		return StatusResult{From: pl.From, To: pl.To, Change: done}, nil
	}
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
	}, reason, token)
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

// changeGiveUp bounds how long a due change waits for a service it needs
// before it fails, when nothing of it was applied yet.
const changeGiveUp = time.Hour

// ApplyDueChanges applies the scheduled changes whose time has come
// (admin-service runs it every few seconds) and returns how many it
// settled. Nothing waits on another service inside the console's
// transaction (C5.5 ⑩): a round claims the due changes (applying_at,
// which also stops their cancel), applies each, then records how it went.
// A change that cannot be applied for a moment (a service down or in
// error) waits for the next round; when the call that applies it was made
// and its answer lost, it stays claimed and the next round checks it: in
// effect already, it is recorded as applied, as one applied whose record
// failed is. A change that is no longer what was confirmed fails.
func (s *Service) ApplyDueChanges(ctx context.Context) (int, error) {
	type claimed struct {
		c domain.InstrumentChange
		// before: a round claimed it earlier and its outcome is unknown,
		// so it counts as attempted whatever this round reaches (C5.5 ⑲).
		before bool
	}
	var due []claimed
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		list, err := r.Changes().Due(ctx, s.Now(), dueBatch)
		if err != nil {
			return err
		}
		for _, c := range list {
			fresh := c.Claim(s.Now())
			if err := r.Changes().Update(ctx, c); err != nil {
				return err
			}
			due = append(due, claimed{c: c, before: !fresh})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range due {
		c := x.c
		result, attempted, err := s.applyChange(ctx, c)
		attempted = attempted || x.before
		if waits(err) && (attempted || s.Now().Sub(c.EffectiveAt) < changeGiveUp) {
			s.Log.WarnContext(ctx, "instruments: change not applied yet", "change_id", c.ID, "attempted", attempted, "error", err)
			if err := s.changeWaits(ctx, c.ID, !attempted, err.Error()); err != nil {
				return n, err
			}
			continue
		}
		applied := err == nil
		if err != nil {
			result = err.Error()
		}
		if err := s.settleChange(ctx, c.ID, applied, result); err != nil {
			if applied {
				// In effect, unrecorded: the next round finds it claimed and
				// in effect, and records it.
				s.Log.ErrorContext(ctx, "instruments: change applied, unrecorded", "change_id", c.ID, "error", err)
			}
			return n, err
		}
		n++
	}
	return n, nil
}

// waits reports whether an apply's error is a moment's: a service down or
// in error, whose outcome the next round finds out.
func waits(err error) bool {
	if err == nil {
		return false
	}
	k := apperr.From(err).Kind
	return k == apperr.KindUnavailable || k == apperr.KindInternal
}

// changeWaits records why a claimed change waits; release gives the claim
// up (nothing of it was applied: it may be canceled again).
func (s *Service) changeWaits(ctx context.Context, id string, release bool, why string) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		c, err := r.Changes().GetForUpdate(ctx, id)
		if err != nil || c == nil || c.Status != domain.ChangeScheduled {
			return err
		}
		if release {
			c.Wait(why)
		} else {
			c.Result = "outcome unknown, checked next round: " + why
		}
		return r.Changes().Update(ctx, *c)
	})
}

// settleChange records how applying a change went, with its audit event.
func (s *Service) settleChange(ctx context.Context, id string, applied bool, result string) error {
	return s.Store.Tx(ctx, func(r ports.Repos) error {
		c, err := r.Changes().GetForUpdate(ctx, id)
		if err != nil || c == nil || c.Status != domain.ChangeScheduled {
			return err
		}
		c.Settle(applied, result, s.Now())
		if err := r.Changes().Update(ctx, *c); err != nil {
			return err
		}
		action := "admin.instruments.change_applied"
		if !applied {
			action = "admin.instruments.change_failed"
		}
		details, _ := json.Marshal(map[string]any{"change_id": c.ID, "kind": c.Kind, "result": c.Result, "approved_by": c.ApprovedByEmail})
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: c.Target, Action: action, Actor: c.RequestedByEmail, Reason: c.Reason, Details: string(details),
		}, c.RequestedByEmail)
	})
}

// errNotConfirmed fails a change whose target moved since it was
// confirmed.
var errNotConfirmed = apperr.New(apperr.KindConflict, apperr.CodeConflict,
	"the reference data changed since the change was confirmed; preview it again")

// errImpactGrew fails a ladder change that would liquidate more
// positions when due than its confirmation showed.
var errImpactGrew = apperr.New(apperr.KindConflict, "ADMIN_IMPACT_GREW",
	"the new risk ladder would liquidate more positions now than its confirmation showed; preview it again")

// inEffect ends the result of a change found in effect already: a round
// before applied it and its record failed, or something else did it.
const inEffect = " (in effect already)"

// applyChange carries out a due change in the requester's name; attempted
// says the call that applies it was made, so with an error its outcome is
// unknown. A change in effect already is applied; a ladder is measured
// again first (C5.5 ⑩).
func (s *Service) applyChange(ctx context.Context, c domain.InstrumentChange) (result string, attempted bool, err error) {
	var sum changeSummary
	if err := json.Unmarshal(c.Summary, &sum); err != nil {
		return "", false, apperr.Invalid("the change's summary is unreadable")
	}
	switch c.Kind {
	case domain.ChangePairStatus, domain.ChangeContractStatus:
		var pl struct{ Symbol, From, To string }
		if err := json.Unmarshal(c.Payload, &pl); err != nil {
			return "", false, apperr.Invalid("the change's payload is unreadable")
		}
		done := fmt.Sprintf("%s: %s → %s", pl.Symbol, pl.From, pl.To)
		cur, err := s.currentStatus(ctx, c.Kind, pl.Symbol)
		switch {
		case err != nil:
			return "", false, err
		case cur == pl.To:
			return done + inEffect, false, nil
		case cur != pl.From:
			return "", false, errNotConfirmed
		}
		if _, err := s.moveStatus(ctx, c.Kind, pl.Symbol, pl.To, c.Reason, c.RequestedByEmail); err != nil {
			return "", true, err
		}
		return done, true, nil
	case domain.ChangeConfig:
		var doc referenced
		if err := json.Unmarshal(c.Payload, &doc); err != nil {
			return "", false, apperr.Invalid("the change's document is unreadable")
		}
		if _, err := s.checkReferences(ctx, doc); err != nil {
			return "", false, err
		}
		dry, err := s.Catalog.Apply(ctx, c.Payload, true, c.RequestedByEmail, c.Reason)
		switch {
		case err != nil:
			return "", false, err
		case len(dry.Changes) == 0:
			return fmt.Sprintf("0 changed, %d unchanged", dry.Unchanged) + inEffect, false, nil
		case fingerprintOf(dry.Changes) != sum.Fingerprint:
			return "", false, errNotConfirmed
		}
		if err := s.measureAgain(ctx, sum); err != nil {
			return "", false, err
		}
		res, err := s.Catalog.Apply(ctx, c.Payload, false, c.RequestedByEmail, c.Reason)
		if err != nil {
			return "", true, err
		}
		return fmt.Sprintf("%d changed, %d unchanged", len(res.Changes), res.Unchanged), true, nil
	}
	return "", false, apperr.Invalid("unknown change kind " + c.Kind)
}

// measureAgain measures a change's new ladders when it is due: one that
// cannot be measured now waits, one that would liquidate more positions
// than its confirmation showed fails.
func (s *Service) measureAgain(ctx context.Context, sum changeSummary) error {
	shown := map[string]int{}
	for _, imp := range sum.Impacts {
		shown[imp.Symbol] = imp.Liquidated
	}
	for _, pc := range sum.Params {
		if pc.Entity != "CONTRACT" || pc.Field != "risk_tiers" {
			continue
		}
		imp, err := s.tierImpact(ctx, pc)
		if err != nil {
			return apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the new risk ladder cannot be measured now")
		}
		if imp.Unmeasured > 0 {
			return apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable,
				fmt.Sprintf("%s: %d positions cannot be measured now", pc.Key, imp.Unmeasured))
		}
		if imp.Liquidated > shown[pc.Key] {
			return errImpactGrew.WithDetail("symbol", pc.Key).WithDetail("liquidated", imp.Liquidated).WithDetail("confirmed", shown[pc.Key])
		}
	}
	return nil
}
