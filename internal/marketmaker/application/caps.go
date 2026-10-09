package application

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/marketmaker/domain"
	"github.com/skill/exchange/internal/marketmaker/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// capsReload is how often the stored caps are read again: a change made
// on another instance (or in the database) reaches the publisher within
// it; one made here at once.
const capsReload = 10 * time.Second

// Caps keeps HOUSE's caps at runtime (the user's decision of 2026-10-07,
// review C45): stored in the marketmaker schema, changed from the console
// (two people there, A69) through the internal API, and handed to the
// publisher, which uses them from its next round. The environment's
// (HOUSE_*) are the first ones stored.
type Caps struct {
	store ports.CapsStore
	pub   *Publisher
	log   *slog.Logger
	now   func() time.Time

	mu      sync.Mutex
	current domain.StoredCaps
}

// NewCaps returns the runtime caps of pub, kept in store.
func NewCaps(store ports.CapsStore, pub *Publisher, log *slog.Logger) *Caps {
	return &Caps{store: store, pub: pub, log: log, now: time.Now}
}

// Start stores defaults (the environment's) unless caps are stored, and
// applies what is stored.
func (c *Caps) Start(ctx context.Context, defaults domain.Caps) error {
	stored, err := c.store.Seed(ctx, defaults, "environment", c.now())
	if err != nil {
		return err
	}
	c.apply(ctx, stored)
	return nil
}

// Get returns the caps in force.
func (c *Caps) Get() domain.StoredCaps {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// Change checks, stores and applies a change: the store applies it to the
// caps it locks (out of bounds or too large a step is refused there); one
// made on caps that changed meanwhile is refused (domain.ErrCapsVersion).
// A new contract leverage is bounded by the highest leverage of the
// contracts HOUSE quotes, read from their specs then (review C73).
func (c *Caps) Change(ctx context.Context, ch domain.CapsChange) (domain.StoredCaps, error) {
	if err := ch.Validate(); err != nil {
		return domain.StoredCaps{}, err
	}
	if ch.Patch.ContractLeverage != nil {
		highest, err := c.highestLeverage(ctx)
		if err != nil {
			return domain.StoredCaps{}, err
		}
		ch.MaxLeverage = highest
	}
	stored, err := c.store.Change(ctx, ch, c.now())
	if err != nil {
		return domain.StoredCaps{}, err
	}
	c.log.InfoContext(ctx, "house caps changed", "version", stored.Version, "actor", ch.Actor, "approver", ch.Approver,
		"approval_id", ch.ApprovalID, "reason", ch.Reason, "signed_by", ch.SignedBy)
	c.apply(ctx, stored)
	return stored, nil
}

// highestLeverage is the highest leverage of the contracts HOUSE quotes
// (their specs' max_leverage), zero when it quotes none.
func (c *Caps) highestLeverage(ctx context.Context) (decimal.Decimal, error) {
	specs, err := c.pub.specs.Specs(ctx)
	if err != nil {
		return decimal.Zero, apperr.Unavailable(fmt.Errorf("contract specs: %w", err))
	}
	var high int32
	for _, s := range specs {
		if s.Contract {
			high = max(high, s.MaxLeverage)
		}
	}
	return decimal.NewFromInt32(high), nil
}

// Changes returns the latest changes, newest first: 20 unless told, at
// most 100.
func (c *Caps) Changes(ctx context.Context, limit int) ([]domain.CapsRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 100)
	return c.store.Changes(ctx, limit)
}

// Run reads the stored caps every capsReload (an app.Loop body); a failed
// read keeps those in force.
func (c *Caps) Run(ctx context.Context) error {
	t := time.NewTicker(capsReload)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		stored, ok, err := c.store.Caps(ctx)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				c.log.WarnContext(ctx, "house caps not read again", "error", err)
			}
		case ok:
			c.apply(ctx, stored)
		}
	}
}

// apply hands newer caps to the publisher.
func (c *Caps) apply(ctx context.Context, stored domain.StoredCaps) {
	c.mu.Lock()
	newer := stored.Version > c.current.Version
	if newer {
		c.current = stored
	}
	c.mu.Unlock()
	if !newer {
		return
	}
	c.pub.SetCaps(stored.Caps, stored.Version)
	cp := stored.Caps
	c.log.InfoContext(ctx, "house liquidity caps", "version", stored.Version, "level", cp.Level.String(), "symbol", cp.Symbol.String(),
		"total", cp.Total.String(), "contract", cp.Contract.String(), "safety", cp.Safety.String(),
		"contract_leverage", cp.ContractLeverage.String())
}
