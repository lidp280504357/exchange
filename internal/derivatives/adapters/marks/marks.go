// Package marks keeps the contracts' latest mark prices, which
// market-data-service publishes every second on market.candle.events.
package marks

import (
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/derivatives/ports"
)

// MaxAge is how old a mark price may be and still count (§11.7: the mark
// price timing out after 10 seconds degrades the contract).
const MaxAge = 10 * time.Second

// Book implements ports.Marks; it is safe for concurrent use.
type Book struct {
	now func() time.Time

	mu    sync.RWMutex
	marks map[string]ports.Mark
}

// New returns an empty book.
func New() *Book { return &Book{now: time.Now, marks: map[string]ports.Mark{}} }

// Set records a mark price computed at at; an older one than the stored is
// ignored.
func (b *Book) Set(symbol string, price decimal.Decimal, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cur, ok := b.marks[symbol]; ok && at.Before(cur.At) {
		return
	}
	b.marks[symbol] = ports.Mark{Price: price, At: at}
}

// Mark returns the latest mark price and whether it is fresh.
func (b *Book) Mark(symbol string) (ports.Mark, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	m, ok := b.marks[symbol]
	return m, ok && b.now().Sub(m.At) <= MaxAge
}

// Age returns how old each contract's mark price is.
func (b *Book) Age() map[string]time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[string]time.Duration, len(b.marks))
	for s, m := range b.marks {
		out[s] = b.now().Sub(m.At)
	}
	return out
}
