package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeMode struct {
	test  bool
	err   error
	reads int
	boom  bool
}

func (f *fakeMode) TestMode(context.Context) (bool, error) {
	f.reads++
	if f.boom {
		f.boom = false
		panic("the profile client broke")
	}
	return f.test, f.err
}

// The exchange's mode for the content (design 2026-10-04 §4.4): live until
// a first read; read every 10 seconds; a failed read keeps the last mode
// and is not tried again for 5 seconds.
func TestContentMode(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	src := &fakeMode{err: errors.New("down")}
	m := &Mode{Profile: src, Now: func() time.Time { return now }}
	ctx := context.Background()

	if m.Test(ctx) || src.reads != 1 {
		t.Fatalf("unread: live after %d reads", src.reads)
	}
	now = now.Add(4 * time.Second)
	if m.Test(ctx); src.reads != 1 {
		t.Fatalf("tried again within 5 seconds: %d reads", src.reads)
	}
	now = now.Add(2 * time.Second)
	src.test, src.err = true, nil
	if !m.Test(ctx) || src.reads != 2 {
		t.Fatalf("read: test mode after %d reads", src.reads)
	}
	now = now.Add(9 * time.Second)
	src.test = false
	if !m.Test(ctx) || src.reads != 2 {
		t.Fatalf("within 10 seconds the mode read stands (%d reads)", src.reads)
	}
	now = now.Add(2 * time.Second)
	src.err = errors.New("down")
	if !m.Test(ctx) || src.reads != 3 {
		t.Fatalf("a failed read keeps the last mode (%d reads)", src.reads)
	}
	now = now.Add(6 * time.Second)
	src.err = nil
	if m.Test(ctx) || src.reads != 4 {
		t.Fatalf("switched live (%d reads)", src.reads)
	}

	// A read that panics leaves the next caller free to read.
	now = now.Add(11 * time.Second)
	src.boom, src.test = true, true
	func() {
		defer func() { _ = recover() }()
		m.Test(ctx)
	}()
	if !m.Test(ctx) || src.reads != 6 {
		t.Fatalf("after a panic (%d reads)", src.reads)
	}
}
