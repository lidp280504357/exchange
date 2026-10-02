package application

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// Editing the reference data (design 2026-10-02 §4.4, C3): the console
// reads it as a config document (the shape of deploy/instruments/<env>.json),
// edits items or pastes new ones, previews what a document changes and
// applies it through instrument-service (source CONSOLE). A later deploy's
// sync keeps what the console changed last.

// maxConfig bounds a document the console submits.
const maxConfig = 1 << 20

// ErrReferenceUnknown refuses a pair following a symbol the reference
// market does not list: it would fail the batched reads of every pair.
var ErrReferenceUnknown = apperr.New(apperr.KindUnprocessable, "ADMIN_REFERENCE_UNKNOWN", "the reference market does not list this symbol")

// houseFlag is the flag whose symbol rules say which pairs and contracts
// HOUSE quotes (ADR-0015).
const houseFlag = "market.house_liquidity"

// InstrumentConfig returns the reference data as a config document.
func (s *Service) InstrumentConfig(ctx context.Context, p Principal) (json.RawMessage, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return nil, err
	}
	return s.Catalog.Export(ctx)
}

// PreviewConfig works out what a config document would change, with the
// console's notes, changing nothing.
func (s *Service) PreviewConfig(ctx context.Context, p Principal, config json.RawMessage) (ports.ConfigResult, error) {
	return s.applyConfig(ctx, p, config, "preview", true)
}

// ApplyConfig applies a config document (audited as
// admin.instruments.applied with the items it changed).
func (s *Service) ApplyConfig(ctx context.Context, p Principal, config json.RawMessage, reason string) (ports.ConfigResult, error) {
	if err := needReason(reason); err != nil {
		return ports.ConfigResult{}, err
	}
	res, err := s.applyConfig(ctx, p, config, strings.TrimSpace(reason), false)
	if err != nil {
		return ports.ConfigResult{}, err
	}
	type item struct {
		Entity  string `json:"entity"`
		Key     string `json:"key"`
		Action  string `json:"action"`
		Version int64  `json:"version"`
	}
	items := make([]item, 0, len(res.Changes))
	for _, c := range res.Changes {
		items = append(items, item{Entity: c.Entity, Key: c.Key, Action: c.Action, Version: c.Version})
	}
	details, _ := json.Marshal(map[string]any{"changes": items, "unchanged": res.Unchanged})
	return res, s.audit(ctx, p, "instruments", "admin.instruments.applied", reason, string(details))
}

// referenced is what the checks read of a document.
type referenced struct {
	Pairs []struct {
		Symbol          string `json:"symbol"`
		ReferenceSymbol string `json:"reference_symbol"`
	} `json:"pairs"`
	Contracts []struct {
		Symbol      string `json:"symbol"`
		IndexSymbol string `json:"index_symbol"`
	} `json:"contracts"`
}

func (s *Service) applyConfig(ctx context.Context, p Principal, config json.RawMessage, reason string, dryRun bool) (ports.ConfigResult, error) {
	if err := p.require(domain.PermInstrumentsEdit); err != nil {
		return ports.ConfigResult{}, err
	}
	var doc referenced
	if len(config) == 0 || len(config) > maxConfig || json.Unmarshal(config, &doc) != nil {
		return ports.ConfigResult{}, apperr.Invalid("config must be a JSON document of at most 1 MB")
	}
	warnings, err := s.checkReferences(ctx, doc)
	if err != nil {
		return ports.ConfigResult{}, err
	}
	res, err := s.Catalog.Apply(ctx, config, dryRun, p.Admin.Email, reason)
	if err != nil {
		return ports.ConfigResult{}, err
	}
	res.Warnings = append(res.Warnings, warnings...)
	return res, nil
}

// checkReferences checks the reference symbols a document brings in (new
// pairs, or pairs following another symbol): the reference market must
// list them on its spot market. It notes what follows: the reference
// streams reconnect, HOUSE quotes a pair or a contract only on its flag's
// list, and a contract only if its index pair's symbol has futures.
func (s *Service) checkReferences(ctx context.Context, doc referenced) ([]ports.ConfigWarning, error) {
	warnings := []ports.ConfigWarning{}
	if len(doc.Pairs) == 0 && len(doc.Contracts) == 0 {
		return warnings, nil
	}
	current, err := s.currentReferences(ctx)
	if err != nil {
		return nil, err
	}
	house := s.houseSymbols(ctx)
	note := func(code, symbol, detail string) {
		warnings = append(warnings, ports.ConfigWarning{Code: code, Symbol: symbol, Detail: detail})
	}
	futures := map[string]bool{}
	reconnect := false
	for _, pair := range doc.Pairs {
		ref := strings.ToUpper(strings.TrimSpace(pair.ReferenceSymbol))
		if ref == "" {
			continue
		}
		if was, ok := current.pairs[pair.Symbol]; ok && was == ref {
			continue
		}
		if s.Reference == nil {
			note(ports.WarnReferenceUnchecked, pair.Symbol, ref)
			continue
		}
		spot, fut, err := s.Reference.Listed(ctx, ref)
		if err != nil {
			return nil, err
		}
		if !spot {
			return nil, ErrReferenceUnknown.WithDetail("symbol", pair.Symbol).WithDetail("reference_symbol", ref)
		}
		futures[ref] = fut
		reconnect = true
		if !house(pair.Symbol) {
			note(ports.WarnHouseNotListed, pair.Symbol, houseFlag)
		}
	}
	for _, c := range doc.Contracts {
		if was, ok := current.contracts[c.Symbol]; ok && was == c.IndexSymbol {
			continue
		}
		ref := current.pairs[c.IndexSymbol]
		for _, pair := range doc.Pairs {
			if pair.Symbol == c.IndexSymbol && pair.ReferenceSymbol != "" {
				ref = strings.ToUpper(strings.TrimSpace(pair.ReferenceSymbol))
			}
		}
		switch {
		case ref == "":
			note(ports.WarnNoIndexReference, c.Symbol, c.IndexSymbol)
			continue
		case s.Reference == nil:
			continue
		}
		fut, ok := futures[ref]
		if !ok {
			var err error
			if _, fut, err = s.Reference.Listed(ctx, ref); err != nil {
				return nil, err
			}
		}
		if !fut {
			note(ports.WarnNoFutures, c.Symbol, ref)
		}
		reconnect = true
		if !house(c.Symbol) {
			note(ports.WarnHouseNotListed, c.Symbol, houseFlag)
		}
	}
	if reconnect {
		note(ports.WarnStreamsReconnect, "", "")
	}
	return warnings, nil
}

// references are the stored pairs' reference symbols and the contracts'
// index pairs.
type references struct {
	pairs     map[string]string
	contracts map[string]string
}

func (s *Service) currentReferences(ctx context.Context) (references, error) {
	raw, err := s.Catalog.Export(ctx)
	if err != nil {
		return references{}, err
	}
	var doc referenced
	if err := json.Unmarshal(raw, &doc); err != nil {
		return references{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "instrument-service answered badly")
	}
	out := references{pairs: map[string]string{}, contracts: map[string]string{}}
	for _, p := range doc.Pairs {
		out.pairs[p.Symbol] = strings.ToUpper(p.ReferenceSymbol)
	}
	for _, c := range doc.Contracts {
		out.contracts[c.Symbol] = c.IndexSymbol
	}
	return out, nil
}

// houseSymbols tells whether HOUSE's flag would cover a symbol: on, and
// its symbol rules permit it. Unknown when the flags cannot be read: then
// everything counts as covered (no note).
func (s *Service) houseSymbols(ctx context.Context) func(string) bool {
	all := func(string) bool { return true }
	if s.Flags == nil {
		return all
	}
	list, err := s.Flags.List(ctx)
	if err != nil {
		return all
	}
	for _, f := range list {
		if f.Key != houseFlag {
			continue
		}
		var rules struct {
			Symbols *struct {
				Allow []string `json:"allow"`
				Deny  []string `json:"deny"`
			} `json:"symbols"`
		}
		_ = json.Unmarshal(f.Rules, &rules)
		return func(symbol string) bool {
			if !f.Enabled {
				return false
			}
			l := rules.Symbols
			if l == nil || (len(l.Allow) == 0 && len(l.Deny) == 0) {
				return true
			}
			return !slices.Contains(l.Deny, symbol) && (len(l.Allow) == 0 || slices.Contains(l.Allow, symbol))
		}
	}
	return func(string) bool { return false } // never set: off
}
