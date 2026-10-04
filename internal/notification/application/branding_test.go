package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeName struct {
	name  string
	err   error
	reads int
	// hold, when set, keeps a read waiting until it is closed.
	hold chan struct{}
	// boom makes the next read panic.
	boom bool
}

func (f *fakeName) PlatformName(context.Context) (string, error) {
	f.reads++
	if f.boom {
		f.boom = false
		panic("the profile client broke")
	}
	if f.hold != nil {
		<-f.hold
	}
	return f.name, f.err
}

// The exchange's name in messages (design 2026-10-04 §4.5): read once per
// ten minutes, the last one kept through a failed read (tried again a
// minute later), and nobody waits for a read another caller is making
// (review BD).
func TestBrandingName(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	src := &fakeName{name: "Example"}
	b := &Branding{Profile: src, Now: func() time.Time { return now }}
	ctx := context.Background()

	if got := b.Name(ctx); got != "Example" || src.reads != 1 {
		t.Fatalf("first %q after %d reads", got, src.reads)
	}
	now = now.Add(9 * time.Minute)
	if got := b.Name(ctx); got != "Example" || src.reads != 1 {
		t.Fatalf("within ten minutes %q after %d reads", got, src.reads)
	}
	now = now.Add(2 * time.Minute)
	src.name, src.err = "", errors.New("down")
	if got := b.Name(ctx); got != "Example" || src.reads != 2 {
		t.Fatalf("a failed read %q after %d reads", got, src.reads)
	}
	now = now.Add(30 * time.Second)
	if b.Name(ctx); src.reads != 2 {
		t.Fatalf("tried again within the minute: %d reads", src.reads)
	}
	now = now.Add(31 * time.Second)
	src.name, src.err = "Renamed", nil
	if got := b.Name(ctx); got != "Renamed" || src.reads != 3 {
		t.Fatalf("a minute after the failure %q after %d reads", got, src.reads)
	}

	// A read in progress: the others go on with the name they have.
	now = now.Add(11 * time.Minute)
	src.name, src.hold = "Later", make(chan struct{})
	done := make(chan string)
	go func() { done <- b.Name(ctx) }()
	for {
		b.mu.Lock()
		reading := b.reading
		b.mu.Unlock()
		if reading {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := b.Name(ctx); got != "Renamed" {
		t.Fatalf("while another reads %q", got)
	}
	close(src.hold)
	if got := <-done; got != "Later" || src.reads != 4 {
		t.Fatalf("the read %q after %d reads", got, src.reads)
	}

	// A read that panics leaves the next caller free to read (review BF).
	now = now.Add(11 * time.Minute)
	src.hold, src.boom = nil, true
	func() {
		defer func() { _ = recover() }()
		b.Name(ctx)
	}()
	if got := b.Name(ctx); got != "Later" || src.reads != 6 {
		t.Fatalf("after a panic %q after %d reads", got, src.reads)
	}

	// Never read: the default (empty).
	fresh := &Branding{Profile: &fakeName{err: errors.New("down")}, Now: func() time.Time { return now }}
	if got := fresh.Name(ctx); got != "" {
		t.Fatalf("without a name %q", got)
	}
}
