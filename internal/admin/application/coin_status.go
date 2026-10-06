package application

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// A coin's contracts closed or reopened at once (coin-margined design
// 2026-10-06 §3.5, coordinator 2026-10-06 22:50, A63): closing puts its
// USDⓈ-M and COIN-M perpetuals that are not delisted in CANCEL_ONLY
// (reduce only; HOUSE keeps quoting so that positions close, its
// market.house_liquidity rules left alone until a contract is delisted),
// reopening puts the closed ones back in TRADING (B122). One change for
// all of them, under the guard of a contract's status: confirmed from the
// preview, after the delay and, two-person, a second ADMIN's approval;
// when due each contract moves and is audited on its own.

// coinMoves are the statuses a coin's contracts move from, by where they
// go: closing takes those trading or halted, reopening those closed (a
// halted one is resumed on its own, one in preparation opened on its own).
var coinMoves = map[string][]string{"CANCEL_ONLY": {"TRADING", statusHalt}, "TRADING": {"CANCEL_ONLY"}}

// CoinContractMove is one of a coin's contracts as a change moves it (or,
// staying, where it is: From and To alike).
type CoinContractMove struct {
	Symbol     string `json:"symbol"`
	MarginType string `json:"margin_type"`
	From       string `json:"from"`
	To         string `json:"to"`
}

// CoinStatusPreview is what closing or reopening a coin's contracts does.
type CoinStatusPreview struct {
	Coin string `json:"coin"`
	To   string `json:"to"`
	// Contracts move; Staying are the coin's others, not delisted.
	Contracts    []CoinContractMove `json:"contracts"`
	Staying      []CoinContractMove `json:"staying"`
	Confirmation *Confirmation      `json:"confirmation"`
	DelaySeconds int                `json:"delay_seconds"`
	TwoPerson    bool               `json:"two_person"`
}

// CoinStatusResult is the change that closes or reopens a coin's
// contracts.
type CoinStatusResult struct {
	Coin      string
	To        string
	Contracts []CoinContractMove
	Change    domain.InstrumentChange
}

// coinPayload is a COIN_CONTRACTS_STATUS change's payload.
type coinPayload struct {
	Coin      string             `json:"coin"`
	To        string             `json:"to"`
	Contracts []CoinContractMove `json:"contracts"`
}

// coinFingerprint binds a confirmation to the moves the preview showed.
func coinFingerprint(coin, to string, moves []CoinContractMove) string {
	parts := []string{domain.ChangeCoinContractsStatus, coin, to}
	for _, m := range moves {
		parts = append(parts, m.Symbol+":"+m.From)
	}
	return strings.Join(parts, "|")
}

// coinPlan works out which of a coin's contracts move to a status.
func (s *Service) coinPlan(ctx context.Context, coin, to string) (moves, staying []CoinContractMove, err error) {
	from, ok := coinMoves[to]
	if !ok {
		return nil, nil, apperr.Invalid("to must be CANCEL_ONLY (close) or TRADING (reopen)")
	}
	contracts, err := s.listedContracts(ctx)
	if err != nil {
		return nil, nil, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the contracts cannot be read")
	}
	moves, staying = []CoinContractMove{}, []CoinContractMove{}
	for _, c := range contracts {
		if c.BaseAsset != coin || c.Status == "DELISTED" {
			continue
		}
		m := CoinContractMove{Symbol: c.Symbol, MarginType: c.marginType(), From: c.Status, To: c.Status}
		if slices.Contains(from, c.Status) {
			m.To = to
			moves = append(moves, m)
		} else {
			staying = append(staying, m)
		}
	}
	bySymbol := func(a, b CoinContractMove) int { return strings.Compare(a.Symbol, b.Symbol) }
	slices.SortFunc(moves, bySymbol)
	slices.SortFunc(staying, bySymbol)
	switch {
	case len(moves)+len(staying) == 0:
		return nil, nil, apperr.NotFound("no contracts of " + coin)
	case len(moves) == 0:
		return nil, nil, apperr.New(apperr.KindConflict, "INSTRUMENT_STATUS_TRANSITION_INVALID",
			fmt.Sprintf("no contract of %s can move to %s", coin, to))
	}
	return moves, staying, nil
}

// PreviewCoinStatus shows which of a coin's contracts closing (to
// CANCEL_ONLY) or reopening (to TRADING) moves, with the confirmation of
// the one change it makes.
func (s *Service) PreviewCoinStatus(ctx context.Context, p Principal, coin, to string) (CoinStatusPreview, error) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return CoinStatusPreview{}, err
	}
	coin, to = strings.ToUpper(strings.TrimSpace(coin)), strings.ToUpper(strings.TrimSpace(to))
	if !assetRE.MatchString(coin) {
		return CoinStatusPreview{}, apperr.Invalid("coin must be an asset code")
	}
	moves, staying, err := s.coinPlan(ctx, coin, to)
	if err != nil {
		return CoinStatusPreview{}, err
	}
	delay, err := s.changeDelay(ctx)
	if err != nil {
		return CoinStatusPreview{}, err
	}
	return CoinStatusPreview{
		Coin: coin, To: to, Contracts: moves, Staying: staying, DelaySeconds: int(delay / time.Second), TwoPerson: s.TwoPerson(),
		Confirmation: s.confirmation(p, domain.ChangeCoinContractsStatus, coinFingerprint(coin, to, moves)),
	}, nil
}

// SetCoinStatus closes or reopens a coin's contracts as its preview
// showed: one change, waiting (and, two-person, a second ADMIN's
// approval); brought again, its confirmation answers with that change.
func (s *Service) SetCoinStatus(ctx context.Context, p Principal, coin, to, reason, token string) (CoinStatusResult, error) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return CoinStatusResult{}, err
	}
	if err := needReason(reason); err != nil {
		return CoinStatusResult{}, err
	}
	reason = strings.TrimSpace(reason)
	if done, err := s.confirmedBefore(ctx, p, token); err != nil || done != nil {
		if err != nil {
			return CoinStatusResult{}, err
		}
		var pl coinPayload
		_ = json.Unmarshal(done.Payload, &pl)
		return CoinStatusResult{Coin: pl.Coin, To: pl.To, Contracts: pl.Contracts, Change: *done}, nil
	}
	prev, err := s.PreviewCoinStatus(ctx, p, coin, to)
	if err != nil {
		return CoinStatusResult{}, err
	}
	fingerprint := coinFingerprint(prev.Coin, prev.To, prev.Contracts)
	if err := s.confirmed(p, token, domain.ChangeCoinContractsStatus, fingerprint); err != nil {
		return CoinStatusResult{}, err
	}
	payload, _ := json.Marshal(coinPayload{Coin: prev.Coin, To: prev.To, Contracts: prev.Contracts})
	params := make([]ParamChange, 0, len(prev.Contracts))
	for _, m := range prev.Contracts {
		params = append(params, ParamChange{
			Entity: "CONTRACT", Key: m.Symbol, Field: "status",
			Before: json.RawMessage(fmt.Sprintf("%q", m.From)), After: json.RawMessage(fmt.Sprintf("%q", m.To)),
		})
	}
	c, err := s.request(ctx, p, domain.ChangeCoinContractsStatus, "coin:"+prev.Coin, payload, changeSummary{
		Fingerprint: fingerprint, Params: params, Impacts: []ports.TierImpact{}, Items: []changeItem{},
	}, reason, token)
	if err != nil {
		return CoinStatusResult{}, err
	}
	return CoinStatusResult{Coin: prev.Coin, To: prev.To, Contracts: prev.Contracts, Change: c}, nil
}

// applyCoinStatus moves a due change's contracts one by one. None moves
// when one of them is no longer where it was confirmed (the change fails:
// preview again), and the result says which are where they go (moved by
// an earlier round of this change, or otherwise) and which are not (review
// EY ①); one found where it goes is in effect already. Each contract's
// status is read again just before its move: one changed since the round
// began is not moved, and one whose move turns out to have started
// elsewhere (changed in between, a move instrument-service allowed) is
// counted as not moved as confirmed (review FD ②); either fails the
// change. Each move is audited as a contract's status move
// (admin.instruments.contract_status). A move failing for a moment waits
// for the next round, which finds those moved in effect; one that will not
// pass is reported in the result with how each of the others went.
func (s *Service) applyCoinStatus(ctx context.Context, c domain.InstrumentChange) (result string, attempted bool, err error) {
	var pl coinPayload
	if err := json.Unmarshal(c.Payload, &pl); err != nil || len(pl.Contracts) == 0 {
		return "", false, apperr.Invalid("the change's payload is unreadable")
	}
	contracts, err := s.listedContracts(ctx)
	if err != nil {
		return "", false, apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the contracts cannot be read")
	}
	now := make(map[string]string, len(contracts))
	for _, x := range contracts {
		now[x.Symbol] = x.Status
	}
	if moved := movedElsewhere(pl.Contracts, now); moved != "" {
		return "", false, apperr.New(apperr.KindConflict, apperr.CodeConflict,
			"the contracts changed since the change was confirmed, nothing more moves; preview it again: "+whereEach(pl.Contracts, now)).
			WithDetail("symbol", moved)
	}
	lines := make([]string, 0, len(pl.Contracts))
	failed := 0
	for _, m := range pl.Contracts {
		line := fmt.Sprintf("%s: %s → %s", m.Symbol, m.From, m.To)
		if now[m.Symbol] == m.To {
			lines = append(lines, line+inEffect)
			continue
		}
		switch cur, err := s.contractStatus(ctx, m.Symbol); {
		case err != nil:
			return "", attempted, err
		case cur == m.To:
			lines = append(lines, line+inEffect)
			continue
		case cur != m.From:
			failed++
			lines = append(lines, notMoved(m.Symbol, cur))
			continue
		}
		attempted = true
		was, err := s.moveStatus(ctx, domain.ChangeContractStatus, m.Symbol, m.To, c.Reason, c.RequestedByEmail)
		if err != nil {
			if waits(err) {
				return "", true, err
			}
			failed++
			lines = append(lines, line+" failed: "+err.Error())
			continue
		}
		if was != "" && was != m.From {
			// Changed between the read and the move: moved, but not as
			// confirmed; audited from where it was.
			failed++
			lines = append(lines, fmt.Sprintf("%s: moved from %s, not %s as confirmed (%s now)", m.Symbol, was, m.From, m.To))
			m.From = was
			s.auditCoinMove(ctx, c, m)
			continue
		}
		s.auditCoinMove(ctx, c, m)
		lines = append(lines, line)
	}
	result = strings.Join(lines, "; ")
	if failed > 0 {
		return "", attempted, apperr.New(apperr.KindConflict, apperr.CodeConflict,
			fmt.Sprintf("%d of %d not moved as confirmed: %s", failed, len(lines), result))
	}
	return result, attempted, nil
}

// contractStatus reads one contract's status now ("" when it is no longer
// listed).
func (s *Service) contractStatus(ctx context.Context, symbol string) (string, error) {
	contracts, err := s.listedContracts(ctx)
	if err != nil {
		return "", apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the contracts cannot be read")
	}
	for _, c := range contracts {
		if c.Symbol == symbol {
			return c.Status, nil
		}
	}
	return "", nil
}

// notMoved says a contract was not moved, and where it is.
func notMoved(symbol, cur string) string {
	if cur == "" {
		return symbol + ": not moved (no longer listed)"
	}
	return fmt.Sprintf("%s: not moved (%s now)", symbol, cur)
}

// movedElsewhere is the first of a change's contracts that is neither
// where it was confirmed nor where it goes ("" for none).
func movedElsewhere(moves []CoinContractMove, now map[string]string) string {
	for _, m := range moves {
		if cur := now[m.Symbol]; cur != m.From && cur != m.To {
			return m.Symbol
		}
	}
	return ""
}

// whereEach says where each of a change's contracts is: where it goes (in
// effect), not moved, or not moved and elsewhere now.
func whereEach(moves []CoinContractMove, now map[string]string) string {
	lines := make([]string, 0, len(moves))
	for _, m := range moves {
		switch cur := now[m.Symbol]; cur {
		case m.To:
			lines = append(lines, fmt.Sprintf("%s: %s → %s%s", m.Symbol, m.From, m.To, inEffect))
		case m.From:
			lines = append(lines, m.Symbol+": not moved")
		default:
			lines = append(lines, notMoved(m.Symbol, cur))
		}
	}
	return strings.Join(lines, "; ")
}

// auditCoinMove records one contract's move of a coin's change in the
// requester's name, as a contract's own status move is.
func (s *Service) auditCoinMove(ctx context.Context, c domain.InstrumentChange, m CoinContractMove) {
	details, _ := json.Marshal(map[string]string{"from": m.From, "to": m.To, "change_id": c.ID, "coin": strings.TrimPrefix(c.Target, "coin:")})
	err := s.Store.Tx(ctx, func(r ports.Repos) error {
		return r.Audit(ctx, &auditv1.AdminActionPerformed{
			Target: "contract:" + m.Symbol, Action: "admin.instruments.contract_status", Actor: c.RequestedByEmail, Reason: c.Reason,
			Details: string(details),
		}, c.RequestedByEmail)
	})
	if err != nil {
		// Moved, its audit event lost: the change's own (applied) lists it.
		s.Log.ErrorContext(ctx, "instruments: a coin's contract moved, unaudited", "change_id", c.ID, "symbol", m.Symbol, "error", err)
	}
}
