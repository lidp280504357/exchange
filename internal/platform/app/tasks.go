package app

import (
	"context"
	"sync"
)

// Tasks runs fire-and-forget work, such as sending a code after the
// response has gone out, as a Component: Stop waits for the tasks in
// flight until its deadline and then cancels them. Register it before the
// servers that spawn tasks, so that it stops after them.
type Tasks struct {
	mu       sync.Mutex
	stopping bool
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewTasks returns an empty task set.
func NewTasks() *Tasks {
	ctx, cancel := context.WithCancel(context.Background())
	return &Tasks{ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// Go runs fn in the background. fn must return when its context ends.
// After Stop has begun, fn runs synchronously with a canceled context, so
// callers never block on a task that cannot start.
func (t *Tasks) Go(fn func(ctx context.Context)) {
	t.mu.Lock()
	if t.stopping {
		t.mu.Unlock()
		fn(t.ctx)
		return
	}
	t.wg.Add(1)
	t.mu.Unlock()
	go func() {
		defer t.wg.Done()
		fn(t.ctx)
	}()
}

// Run blocks until Stop.
func (t *Tasks) Run() error {
	<-t.done
	return nil
}

// Stop waits for running tasks, canceling them when ctx ends first.
func (t *Tasks) Stop(ctx context.Context) error {
	t.mu.Lock()
	if !t.stopping {
		t.stopping = true
		close(t.done)
	}
	t.mu.Unlock()

	finished := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		t.cancel()
		return nil
	case <-ctx.Done():
		t.cancel()
		<-finished
		return ctx.Err()
	}
}
