package application

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

// The bounds of a threshold target's and a spike's own fields (ASTRA
// design §6.2, A6); market-sim checks the rest: the target's feasibility,
// the guards' budget, the spikes per hour, the closing window.
const (
	maxSpikeSize     = 0.10 // a share of the planned price, either way
	maxSpikeWidth    = 60   // seconds
	maxTargetSpikes  = 50   // a sane request; market-sim allows 6 an hour
	minTargetSeconds = 60
	maxTargetSeconds = 24 * 3600 // market-sim's longest window
)

// spikeMarkShare is the share of a spike's size the perpetual's mark
// price takes at most, for its liquidations (ASTRA design §6.2, A6). The
// mark follows the index: the spot's 60-second TWAP averaged with the
// book's middle while that is within 1% of the last trade
// (marketdata's PlatformPrice). The quotes' center reaches the tip in 3
// seconds and comes back over the width, the executors' trades with it,
// so at the tip the index has moved about half of it (the middle, plus a
// TWAP that holds 1.5 seconds of it); without a middle, the TWAP alone
// holds at most 1.5 + width/2 seconds of the tip in its 60.
func spikeMarkShare(width int) float64 {
	if width <= 0 {
		width = 20
	}
	return math.Min(1, math.Max((1+1.5/60)/2, (1.5+float64(width)/2)/60))
}

// simMarkAt is the price a request's impact on the perpetual is measured
// at: a spike's tip as the mark takes it (spikeMarkShare), else where it
// takes the price.
func simMarkAt(a domain.Approval, target, expected decimal.Decimal) decimal.Decimal {
	var change struct {
		Type         string  `json:"type"`
		Size         float64 `json:"size"`
		WidthSeconds int     `json:"width_seconds"`
	}
	if a.Kind != domain.KindSimEvent || json.Unmarshal([]byte(a.Payload["change"]), &change) != nil || change.Type != "SPIKE" {
		return expected
	}
	return target.Mul(decimal.NewFromFloat(1 + change.Size*spikeMarkShare(change.WidthSeconds)))
}

// checkTarget normalizes and checks the fields of a threshold target
// (direction, then, spikes) and of a spike (width), and refuses them on
// any other event.
func (in *SimEventInput) checkTarget(now time.Time) error {
	in.Direction = strings.ToUpper(strings.TrimSpace(in.Direction))
	in.Then = strings.ToUpper(strings.TrimSpace(in.Then))
	switch in.Direction {
	case "", "ABOVE", "BELOW":
	default:
		return apperr.Invalid("direction is ABOVE or BELOW")
	}
	switch in.Then {
	case "", "FOLLOW", "HOLD":
	default:
		return apperr.Invalid("then is FOLLOW or HOLD")
	}
	if in.Type != "TARGET" && (in.Direction != "" || in.Then != "" || len(in.Spikes) > 0) {
		return apperr.Invalid("direction, then and spikes belong to a TARGET")
	}
	if in.WidthSeconds != 0 && (in.Type != "SPIKE" || in.WidthSeconds < 0 || in.WidthSeconds > maxSpikeWidth) {
		return apperr.Invalid("width_seconds, at most 60, belongs to a SPIKE")
	}
	if in.Type == "SPIKE" && (in.Size == nil || *in.Size == 0 || math.Abs(*in.Size) > maxSpikeSize) {
		return apperr.Invalid("a spike's size is a share of the planned price, at most 0.10 either way")
	}
	if len(in.Spikes) > maxTargetSpikes {
		return apperr.Invalid("at most 50 spikes")
	}
	for i := range in.Spikes {
		sp := &in.Spikes[i]
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(sp.At))
		if err != nil {
			return apperr.Invalid("a spike's at must be an RFC 3339 time")
		}
		if !at.After(now) {
			return apperr.Invalid("a spike is planned in the future")
		}
		sp.At = at.UTC().Format(time.RFC3339)
		if sp.Size == 0 || math.Abs(sp.Size) > maxSpikeSize {
			return apperr.Invalid("a spike's size is a share of the planned price, at most 0.10 either way")
		}
		if sp.WidthSeconds < 0 || sp.WidthSeconds > maxSpikeWidth {
			return apperr.Invalid("a spike's width is at most 60 seconds")
		}
	}
	return nil
}

// SimPlan returns a threshold target's plan: its envelope minute by
// minute, its spikes and where the market is against it now (A6).
func (s *Service) SimPlan(ctx context.Context, p Principal, id string) (json.RawMessage, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperr.NotFound("no such event")
	}
	return s.Sim.Plan(ctx, id)
}

// SimTargetPreview previews a threshold target for the console's form:
// whether it is feasible from the current target, the shortest window that
// is, the move it plans, whether a second administrator must approve it,
// and its envelope (A6).
func (s *Service) SimTargetPreview(ctx context.Context, p Principal, q ports.SimTargetQuery) (json.RawMessage, error) {
	if err := p.require(domain.PermReportsRead); err != nil {
		return nil, err
	}
	q.Direction = strings.ToUpper(strings.TrimSpace(q.Direction))
	switch q.Direction {
	case "", "ABOVE", "BELOW":
	default:
		return nil, apperr.Invalid("direction is ABOVE or BELOW")
	}
	price, err := decimal.NewFromString(strings.TrimSpace(q.Price))
	if err != nil || !price.IsPositive() {
		return nil, apperr.Invalid("price is the level, a positive number")
	}
	q.Price = price.String()
	if q.DurationSeconds < minTargetSeconds || q.DurationSeconds > maxTargetSeconds {
		return nil, apperr.Invalid("duration_seconds is at least 60 and at most a day")
	}
	if q.StartsAt = strings.TrimSpace(q.StartsAt); q.StartsAt != "" {
		at, err := time.Parse(time.RFC3339, q.StartsAt)
		if err != nil {
			return nil, apperr.Invalid("starts_at must be an RFC 3339 time")
		}
		q.StartsAt = at.UTC().Format(time.RFC3339)
	}
	return s.Sim.TargetPreview(ctx, q)
}
