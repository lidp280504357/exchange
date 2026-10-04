package application

import (
	"context"
	"sync"
	"time"

	"github.com/skill/exchange/internal/notification/ports"
)

// The exchange's name in messages is read this often from the platform
// profile, and after a failed read retried this much later.
const (
	brandTTL   = 10 * time.Minute
	brandRetry = time.Minute
	// brandWait bounds a read, so no message waits long for its name.
	brandWait = 2 * time.Second
)

// Branding names the exchange in messages (design 2026-10-04 §4.5): the
// platform profile's name, read at most every ten minutes; while it cannot
// be read the last name read, or the default, signs them.
type Branding struct {
	Profile ports.PlatformName
	Now     func() time.Time

	mu      sync.Mutex
	name    string
	readAt  time.Time
	reading bool
}

// Name returns the exchange's name, "" for the default. One caller reads
// the profile when the name is due; the others meanwhile go on with the
// name they have rather than wait for it (review BD).
func (b *Branding) Name(ctx context.Context) string {
	b.mu.Lock()
	now := b.Now()
	if b.reading || (!b.readAt.IsZero() && now.Sub(b.readAt) < brandTTL) {
		defer b.mu.Unlock()
		return b.name
	}
	b.reading = true
	b.mu.Unlock()

	name, err := b.read(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reading = false
	if err != nil || name == "" {
		b.readAt = now.Add(brandRetry - brandTTL)
		return b.name
	}
	b.name, b.readAt = name, now
	return name
}

// read asks the profile for the name. Should the read panic, the next
// caller may read again (review BF); otherwise Name clears reading as it
// keeps the name.
func (b *Branding) read(ctx context.Context) (string, error) {
	returned := false
	defer func() {
		if !returned {
			b.mu.Lock()
			b.reading = false
			b.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, brandWait)
	defer cancel()
	name, err := b.Profile.PlatformName(ctx)
	returned = true
	return name, err
}
