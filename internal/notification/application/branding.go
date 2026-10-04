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

	mu     sync.Mutex
	name   string
	readAt time.Time
}

// Name returns the exchange's name, "" for the default.
func (b *Branding) Name(ctx context.Context) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.Now()
	if !b.readAt.IsZero() && now.Sub(b.readAt) < brandTTL {
		return b.name
	}
	ctx, cancel := context.WithTimeout(ctx, brandWait)
	defer cancel()
	name, err := b.Profile.PlatformName(ctx)
	if err != nil || name == "" {
		b.readAt = now.Add(brandRetry - brandTTL)
		return b.name
	}
	b.name, b.readAt = name, now
	return name
}
