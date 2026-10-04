package application

import (
	"context"
	"sync"
	"time"

	"github.com/skill/exchange/internal/notification/ports"
)

// The exchange's mode is read this often from the platform profile, and
// after a failed read retried this much later.
const (
	modeTTL   = 30 * time.Second
	modeRetry = 5 * time.Second
	// modeWait bounds a read.
	modeWait = 2 * time.Second
)

// Mode says which content the sites get (design 2026-10-04 §4.4): the
// platform profile's test mode, read at most every 30 seconds, so a switch
// of mode swaps the content within a minute (the sites cache 15 seconds).
// Until a first read succeeds every caller tries one; afterwards one
// caller reads when the mode is due and the others go on with the last
// one, which also stands while the profile cannot be read. A failed read
// is not tried again for 5 seconds. Live before any read, as the sites'
// own default.
type Mode struct {
	Profile ports.PlatformMode
	Now     func() time.Time

	mu      sync.Mutex
	test    bool
	readAt  time.Time
	retryAt time.Time
	reading bool
}

// Test reports whether the exchange is in test mode.
func (m *Mode) Test(ctx context.Context) bool {
	m.mu.Lock()
	now := m.Now()
	known := !m.readAt.IsZero()
	if now.Before(m.retryAt) || (known && (m.reading || now.Sub(m.readAt) < modeTTL)) {
		defer m.mu.Unlock()
		return m.test
	}
	m.reading = true
	m.mu.Unlock()

	test, err := m.read(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reading = false
	if err != nil {
		m.retryAt = now.Add(modeRetry)
		return m.test
	}
	m.test, m.readAt = test, now
	return test
}

// read asks the profile; a read that panics leaves the next caller free
// to read (as Branding.read).
func (m *Mode) read(ctx context.Context) (bool, error) {
	returned := false
	defer func() {
		if !returned {
			m.mu.Lock()
			m.reading = false
			m.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, modeWait)
	defer cancel()
	test, err := m.Profile.TestMode(ctx)
	returned = true
	return test, err
}
