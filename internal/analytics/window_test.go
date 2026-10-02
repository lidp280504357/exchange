package analytics

import (
	"testing"
	"time"
)

// The window stays within the outboxes' retention, a day at most.
func TestTheWindowFitsTheRetention(t *testing.T) {
	r := &Reconciler{}
	for retention, want := range map[time.Duration]time.Duration{
		7 * 24 * time.Hour: 24 * time.Hour,
		6 * time.Hour:      5*time.Hour + 20*time.Minute,
		30 * time.Minute:   time.Minute,
	} {
		if r.SetRetention(retention); r.Window != want {
			t.Fatalf("retention %s: window %s, want %s", retention, r.Window, want)
		}
	}
}
