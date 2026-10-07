package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/skill/exchange/internal/admin/domain"
	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The product lines (design 2026-10-07, product switches, K3): spot
// trading and the USDT- and coin-margined contracts, each open unless an
// administrator closes it, never deleted. A line's switch is the flag
// product.<line> (seeded open; one not stored counts as open). Closing a
// line hides it from the sites and refuses its new orders but reduce-only
// closes and cancels (the services, which also sweep what slips in as it
// closes); here its open orders are canceled through the service that runs
// it.

// productLines are the lines in the order the console shows them.
var productLines = []string{"spot", "usdt_m", "coin_m"}

// productFlag is a line's flag.
func productFlag(line string) string { return "product." + line }

// ProductState is a product line: its switch, when and by whom it was last
// switched (nil and "" while never), when it was closed (while it is), and
// what closing it touches now (nil when it could not be read).
type ProductState struct {
	Product       string
	Enabled       bool
	Flag          string
	Version       int64
	SwitchedAt    *time.Time
	SwitchedBy    string
	ClosedAt      *time.Time
	OpenOrders    *int
	OpenPositions *int
}

// Products are the product lines, the lines whose counts could not be read
// (Partial), and the open orders a switch canceled.
type Products struct {
	Lines          []ProductState
	Partial        []string
	CanceledOrders int
}

// Products returns the product lines with what closing each would touch.
func (s *Service) Products(ctx context.Context, p Principal) (Products, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return Products{}, err
	}
	return s.products(ctx)
}

func (s *Service) products(ctx context.Context) (Products, error) {
	all, err := s.Flags.List(ctx)
	if err != nil {
		return Products{}, err
	}
	stored := map[string]ports.Flag{}
	for _, f := range all {
		if f.UpdatedAt != nil {
			stored[f.Key] = f
		}
	}
	out := Products{Lines: make([]ProductState, 0, len(productLines)), Partial: []string{}}
	for _, line := range productLines {
		st := ProductState{Product: line, Flag: productFlag(line), Enabled: true}
		if f, ok := stored[st.Flag]; ok {
			st.Enabled, st.Version, st.SwitchedAt, st.SwitchedBy = f.Enabled, f.Version, f.UpdatedAt, f.UpdatedBy
			if !f.Enabled {
				st.ClosedAt = f.UpdatedAt
			}
		}
		// What closing it touches, as the service that runs it counts it.
		if counts, err := s.productLine(ctx, line); err != nil {
			s.Log.WarnContext(ctx, "product line: counts unknown", "product", line, "error", err)
			out.Partial = append(out.Partial, line)
		} else {
			st.OpenOrders, st.OpenPositions = &counts.OpenOrders, &counts.OpenPositions
		}
		out.Lines = append(out.Lines, st)
	}
	return out, nil
}

// productLine reads a line's counts from its service.
func (s *Service) productLine(ctx context.Context, line string) (ports.ProductLine, error) {
	if s.ProductLines == nil {
		return ports.ProductLine{}, ports.ErrProductLineMissing
	}
	return s.ProductLines.Line(ctx, line)
}

// SetProduct opens or closes a product line: one administrator with
// instruments.trading, a reason, audited as admin.products.toggled.
// Closing it cancels its open orders through its service (which audits
// each cancel); opening it cancels nothing. Switching a line to the state
// it is in changes nothing - but closing a closed line again cancels its
// orders still open (after a cancel that failed, or a service that had no
// endpoint for it yet), audited as admin.products.orders_canceled.
func (s *Service) SetProduct(ctx context.Context, p Principal, product string, enabled bool, reason string) (Products, error) {
	if err := p.require(domain.PermInstrumentsTrading); err != nil {
		return Products{}, err
	}
	if err := needReason(reason); err != nil {
		return Products{}, err
	}
	reason = strings.TrimSpace(reason)
	product = strings.ToLower(strings.TrimSpace(product))
	if !slices.Contains(productLines, product) {
		return Products{}, apperr.Invalid("product must be spot, usdt_m or coin_m")
	}
	cur, err := s.products(ctx)
	if err != nil {
		return Products{}, err
	}
	was := cur.Lines[slices.IndexFunc(cur.Lines, func(l ProductState) bool { return l.Product == product })]
	if was.Enabled == enabled {
		if enabled || was.OpenOrders == nil || *was.OpenOrders == 0 {
			return cur, nil
		}
		// Closed already, orders still open: cancel them.
		n, cancelErr := s.cancelProduct(ctx, p, product, reason)
		s.auditProduct(ctx, p, "admin.products.orders_canceled", product, false, false, n, cancelErr, reason)
		return s.productsAfter(ctx, n)
	}
	if _, err := s.Flags.Switch(ctx, productFlag(product), enabled, p.Admin.Email, reason); err != nil {
		return Products{}, err
	}
	n := 0
	var cancelErr error
	if !enabled {
		n, cancelErr = s.cancelProduct(ctx, p, product, reason)
	}
	s.auditProduct(ctx, p, "admin.products.toggled", product, was.Enabled, enabled, n, cancelErr, reason)
	return s.productsAfter(ctx, n)
}

// cancelProduct asks the line's service to cancel its open orders; its
// failure is logged and audited, the switch standing.
func (s *Service) cancelProduct(ctx context.Context, p Principal, product, reason string) (int, error) {
	if s.ProductLines == nil {
		return 0, ports.ErrProductLineMissing
	}
	n, err := s.ProductLines.CancelOpen(ctx, product, p.Admin.Email, reason)
	if err != nil {
		s.Log.WarnContext(ctx, "product line: its open orders not canceled", "product", product, "error", err)
	}
	return n, err
}

// auditProduct audits a switch (or a cancel of a closed line's orders):
// the line, from and to, the orders canceled and why none could be.
func (s *Service) auditProduct(ctx context.Context, p Principal, action, product string, from, to bool, canceled int, cancelErr error,
	reason string,
) {
	d := map[string]any{"product": product, "flag": productFlag(product), "from": from, "to": to, "canceled_orders": canceled}
	switch {
	case errors.Is(cancelErr, ports.ErrProductLineMissing):
		d["cancel"] = "unavailable"
	case cancelErr != nil:
		d["cancel"] = "failed"
		d["cancel_error"] = cancelErr.Error()
	}
	details, _ := json.Marshal(d)
	if err := s.audit(ctx, p, "product:"+product, action, reason, string(details)); err != nil {
		s.Log.ErrorContext(ctx, "product line: switched, unaudited", "product", product, "error", err)
	}
}

// productsAfter reads the lines after a change, with what it canceled.
func (s *Service) productsAfter(ctx context.Context, canceled int) (Products, error) {
	out, err := s.products(ctx)
	if err != nil {
		return Products{}, err
	}
	out.CanceledOrders = canceled
	return out, nil
}
