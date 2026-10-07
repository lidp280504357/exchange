package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
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
// (Partial), and how the cancel a switch asked for went (nil when it asked
// none).
type Products struct {
	Lines   []ProductState
	Partial []string
	Cancel  *ProductCancel
}

// ProductCancel is how canceling a closed line's open orders went (A85,
// A87): CancelDone; CancelUnavailable, its service having no endpoint for
// it yet; or CancelFailed, with why (Reason) - not answering in time, not
// reached, failing for some users (FailedUsers) or refusing - Canceled
// counting what the service said it canceled, and Error the service's own
// code and message (never an address of ours: the console shows it). The
// line stays closed either way, and closing it again cancels what is left.
type ProductCancel struct {
	Status      string
	Canceled    int
	FailedUsers int
	Reason      string
	Error       string
}

// How a cancel went, and why one failed.
const (
	CancelDone        = "DONE"
	CancelUnavailable = "UNAVAILABLE"
	CancelFailed      = "FAILED"

	CancelTimeout     = "TIMEOUT"
	CancelUnreachable = "UNREACHABLE"
	CancelPartial     = "PARTIAL"
	CancelRefused     = "REFUSED"
)

// Products returns the product lines with what closing each would touch.
func (s *Service) Products(ctx context.Context, p Principal) (Products, error) {
	if err := p.require(domain.PermInstrumentsRead); err != nil {
		return Products{}, err
	}
	return s.products(ctx)
}

func (s *Service) products(ctx context.Context) (Products, error) {
	lines, err := s.productStates(ctx)
	if err != nil {
		return Products{}, err
	}
	// What closing each touches, as the service that runs it counts it,
	// read together (each read waits for its service a few seconds at
	// most: a switch reads them twice within the console's 30 seconds).
	counts := make([]ports.ProductLine, len(lines))
	errs := make([]error, len(lines))
	var wg sync.WaitGroup
	for i := range lines {
		wg.Go(func() { counts[i], errs[i] = s.productLine(ctx, lines[i].Product) })
	}
	wg.Wait()
	out := Products{Lines: lines, Partial: []string{}}
	for i, st := range lines {
		if errs[i] != nil {
			s.Log.WarnContext(ctx, "product line: counts unknown", "product", st.Product, "error", errs[i])
			out.Partial = append(out.Partial, st.Product)
			continue
		}
		out.Lines[i].OpenOrders, out.Lines[i].OpenPositions = &counts[i].OpenOrders, &counts[i].OpenPositions
	}
	return out, nil
}

// productStates are the lines' switches as the flags have them.
func (s *Service) productStates(ctx context.Context) ([]ProductState, error) {
	all, err := s.Flags.List(ctx)
	if err != nil {
		return nil, err
	}
	stored := map[string]ports.Flag{}
	for _, f := range all {
		if f.UpdatedAt != nil {
			stored[f.Key] = f
		}
	}
	lines := make([]ProductState, 0, len(productLines))
	for _, line := range productLines {
		st := ProductState{Product: line, Flag: productFlag(line), Enabled: true}
		if f, ok := stored[st.Flag]; ok {
			st.Enabled, st.Version, st.SwitchedAt, st.SwitchedBy = f.Enabled, f.Version, f.UpdatedAt, f.UpdatedBy
			if !f.Enabled {
				st.ClosedAt = f.UpdatedAt
			}
		}
		lines = append(lines, st)
	}
	return lines, nil
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
// each cancel), the answer saying how that went; opening it cancels
// nothing. Switching a line to the state it is in changes nothing - but
// closing a closed line again cancels its orders still open (after a
// cancel that failed, or a service that had no endpoint for it yet),
// audited as admin.products.orders_canceled.
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
	lines, err := s.productStates(ctx)
	if err != nil {
		return Products{}, err
	}
	was := lines[slices.IndexFunc(lines, func(l ProductState) bool { return l.Product == product })]
	if was.Enabled == enabled {
		if !enabled {
			// Closed already: its orders still open are canceled (none
			// counted, nothing is asked; a count unknown asks anyway).
			if counts, err := s.productLine(ctx, product); err != nil || counts.OpenOrders > 0 {
				c, cause := s.cancelProduct(ctx, p, product, reason)
				s.auditProduct(ctx, p, "admin.products.orders_canceled", product, false, false, c, cause, reason)
				return s.productsAfter(ctx, c)
			}
		}
		return s.products(ctx)
	}
	if _, err := s.Flags.Switch(ctx, productFlag(product), enabled, p.Admin.Email, reason); err != nil {
		return Products{}, err
	}
	var c *ProductCancel
	var cause error
	if !enabled {
		c, cause = s.cancelProduct(ctx, p, product, reason)
	}
	s.auditProduct(ctx, p, "admin.products.toggled", product, was.Enabled, enabled, c, cause, reason)
	return s.productsAfter(ctx, c)
}

// cancelProduct asks the line's service to cancel its open orders and
// says how it went, with the failure in full (logged here, audited by the
// caller); the switch stands.
func (s *Service) cancelProduct(ctx context.Context, p Principal, product, reason string) (*ProductCancel, error) {
	if s.ProductLines == nil {
		return &ProductCancel{Status: CancelUnavailable}, ports.ErrProductLineMissing
	}
	n, err := s.ProductLines.CancelOpen(ctx, product, p.Admin.Email, reason)
	c := &ProductCancel{Status: CancelFailed, Canceled: n.Orders, FailedUsers: n.FailedUsers}
	switch {
	case err == nil:
		return &ProductCancel{Status: CancelDone, Canceled: n.Orders}, nil
	case errors.Is(err, ports.ErrProductLineMissing):
		return &ProductCancel{Status: CancelUnavailable, Canceled: n.Orders}, err
	case errors.Is(err, ports.ErrProductCancelTimeout):
		c.Reason = CancelTimeout
	case errors.Is(err, ports.ErrProductCancelUnreachable):
		c.Reason = CancelUnreachable
	case n.FailedUsers > 0:
		c.Reason = CancelPartial
	default:
		c.Reason = CancelRefused
	}
	// What the service said, if it answered: its code and message.
	if e := apperr.From(err); e != nil && (c.Reason == CancelPartial || c.Reason == CancelRefused) {
		c.Error = e.Code + ": " + e.Message
	}
	s.Log.WarnContext(ctx, "product line: its open orders not all canceled", "product", product, "canceled", n.Orders,
		"failed_users", n.FailedUsers, "error", err)
	return c, err
}

// auditProduct audits a switch (or a cancel of a closed line's orders):
// the line, from and to, the orders canceled and why not all could be (the
// failure in full).
func (s *Service) auditProduct(ctx context.Context, p Principal, action, product string, from, to bool, c *ProductCancel, cause error,
	reason string,
) {
	d := map[string]any{"product": product, "flag": productFlag(product), "from": from, "to": to, "canceled_orders": 0}
	if c != nil {
		d["canceled_orders"] = c.Canceled
		switch c.Status {
		case CancelUnavailable:
			d["cancel"] = "unavailable"
		case CancelFailed:
			d["cancel"], d["cancel_reason"], d["failed_users"] = "failed", c.Reason, c.FailedUsers
			if cause != nil {
				d["cancel_error"] = cause.Error()
			}
		}
	}
	details, _ := json.Marshal(d)
	if err := s.audit(ctx, p, "product:"+product, action, reason, string(details)); err != nil {
		s.Log.ErrorContext(ctx, "product line: switched, unaudited", "product", product, "error", err)
	}
}

// productsAfter reads the lines after a change, with how its cancel went.
func (s *Service) productsAfter(ctx context.Context, c *ProductCancel) (Products, error) {
	out, err := s.products(ctx)
	if err != nil {
		return Products{}, err
	}
	out.Cancel = c
	return out, nil
}
