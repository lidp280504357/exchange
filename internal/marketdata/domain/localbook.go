package domain

import (
	"errors"
	"slices"

	"github.com/shopspring/decimal"
)

// DepthDiff is one update of the reference market's book stream (Binance
// depthUpdate, ADR-0010): the levels that changed (quantity zero removes
// one), covering update IDs First..Last. Prev is the Last of the update
// before it on futures streams (their "pu"), 0 on spot streams.
type DepthDiff struct {
	First, Last, Prev int64
	Bids, Asks        []Level
}

// ErrBookGap means an update does not follow the book: the book must be
// loaded again from a snapshot.
var ErrBookGap = errors.New("reference book: an update is missing")

// LocalBook is a copy of the reference market's order book, kept the way
// Binance documents it: updates are buffered until a REST snapshot
// arrives, the ones it covers are dropped, and the rest are applied in
// order; a gap discards the book. Prices are the reference market's, as
// received (the caller converts). Not safe for concurrent use.
type LocalBook struct {
	lastID  int64
	synced  bool
	futures bool // gaps are told by Prev, not by First
	// first is true from a snapshot until the first update after it: on
	// futures streams that update spans the snapshot and its Prev is
	// older.
	first   bool
	bids    []Level
	asks    []Level
	pending []DepthDiff
}

// NewLocalBook returns an empty book waiting for its snapshot; futures
// books check the continuity of updates by their Prev.
func NewLocalBook(futures bool) *LocalBook { return &LocalBook{futures: futures} }

// Synced reports whether the book holds a snapshot with every update
// since.
func (b *LocalBook) Synced() bool { return b.synced }

// Update applies a stream update, or buffers it while the book waits for
// its snapshot. ErrBookGap means the book was discarded.
func (b *LocalBook) Update(d DepthDiff) error {
	if !b.synced {
		b.pending = append(b.pending, d)
		if len(b.pending) > 1000 { // a snapshot that never came: keep the latest
			b.pending = slices.Delete(b.pending, 0, len(b.pending)-1000)
		}
		return nil
	}
	return b.apply(d)
}

func (b *LocalBook) apply(d DepthDiff) error {
	if d.Last <= b.lastID {
		return nil // already in the book
	}
	gap := d.First > b.lastID+1
	if b.futures {
		// Futures updates follow each other by Prev; the one spanning the
		// snapshot (First <= lastID <= Last) has an older Prev.
		spans := b.first && d.First <= b.lastID
		gap = d.Prev != b.lastID && !spans
	}
	if gap {
		b.Reset()
		return ErrBookGap
	}
	b.first = false
	for _, l := range d.Bids {
		b.bids = set(b.bids, l, true)
	}
	for _, l := range d.Asks {
		b.asks = set(b.asks, l, false)
	}
	b.lastID = d.Last
	return nil
}

// Load takes a REST snapshot at lastID and applies the buffered updates
// after it. ErrBookGap means the snapshot is older than the first buffered
// update (fetch it again) or the updates do not follow it.
func (b *LocalBook) Load(lastID int64, bids, asks []Level) error {
	pending := b.pending
	b.pending = nil
	if len(pending) > 0 && pending[0].First > lastID+1 && !b.futures {
		b.pending = pending // the snapshot is too old for the stream
		return ErrBookGap
	}
	b.lastID, b.synced, b.first = lastID, true, true
	b.bids, b.asks = sortLevels(bids, true), sortLevels(asks, false)
	for _, d := range pending {
		if err := b.apply(d); err != nil {
			return err
		}
	}
	return nil
}

// Reset discards the book: it waits for a snapshot again.
func (b *LocalBook) Reset() {
	b.synced, b.first, b.lastID, b.bids, b.asks, b.pending = false, false, 0, nil, nil, nil
}

// Top returns up to n levels a side, best first.
func (b *LocalBook) Top(n int) (bids, asks []Level) {
	return slices.Clone(b.bids[:min(n, len(b.bids))]), slices.Clone(b.asks[:min(n, len(b.asks))])
}

// set puts a level into a side kept best first (bids falling, asks
// rising); quantity zero removes it.
func set(side []Level, l Level, bids bool) []Level {
	i, found := slices.BinarySearchFunc(side, l.Price, func(e Level, p decimal.Decimal) int {
		c := e.Price.Cmp(p)
		if bids {
			return -c
		}
		return c
	})
	switch {
	case found && !l.Quantity.IsPositive():
		return slices.Delete(side, i, i+1)
	case found:
		side[i].Quantity = l.Quantity
		return side
	case l.Quantity.IsPositive():
		return slices.Insert(side, i, l)
	}
	return side
}

func sortLevels(in []Level, bids bool) []Level {
	out := make([]Level, 0, len(in))
	for _, l := range in {
		if l.Quantity.IsPositive() {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b Level) int {
		c := a.Price.Cmp(b.Price)
		if bids {
			return -c
		}
		return c
	})
	return out
}
