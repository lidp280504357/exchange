package application

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The trading pages of design 2026-10-02 §3 (C3): every user's positions
// and the liquidation log.

// PositionsPage is every user's open positions, riskiest first, with
// HOUSE's account so the console can tell its positions apart.
type PositionsPage struct {
	Positions json.RawMessage `json:"positions"`
	Truncated bool            `json:"truncated"`
	// HouseUserID is HOUSE's account on the contracts; nil when not
	// configured.
	HouseUserID *string `json:"house_user_id"`
	// KindsNarrowed says the kind filter was cut to MaxKindIDs (L1).
	KindsNarrowed bool `json:"kinds_narrowed,omitempty"`
}

// positionsLimit bounds the positions listed; beyond it the page says so.
const positionsLimit = 500

// OpenPositions lists every user's open positions (derivatives-service),
// riskiest first: of a contract, of a user, only those under watch.
func (s *Service) OpenPositions(ctx context.Context, p Principal, q ports.PositionQuery) (PositionsPage, error) {
	if err := p.require(domain.PermDerivativesRead); err != nil {
		return PositionsPage{}, err
	}
	if q.UserID != "" {
		if _, err := uuid.Parse(q.UserID); err != nil {
			return PositionsPage{}, apperr.Invalid("user_id must be a UUID")
		}
	}
	q.Symbol = strings.ToUpper(strings.TrimSpace(q.Symbol))
	if q.Limit <= 0 || q.Limit > positionsLimit {
		q.Limit = positionsLimit
	}
	// The humans' by default (L1): HOUSE's are SYSTEM's.
	var err error
	if q.ByKind, err = s.kindFilter(ctx, q.Kinds, q.UserID, true); err != nil {
		return PositionsPage{}, err
	}
	raw, err := s.Derivatives.OpenPositions(ctx, q)
	if err != nil {
		return PositionsPage{}, err
	}
	var page PositionsPage
	if err := json.Unmarshal(raw, &page); err != nil || page.Positions == nil {
		return PositionsPage{}, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "derivatives-service answered badly")
	}
	page.KindsNarrowed = q.ByKind.Narrowed
	if s.HouseBook.User != "" {
		house := s.HouseBook.User
		page.HouseUserID = &house
	}
	return page, nil
}
