package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestTasksWaitsForRunningWork(t *testing.T) {
	tasks := NewTasks()
	go func() { _ = tasks.Run() }()
	var done atomic.Int32
	for range 3 {
		tasks.Go(func(context.Context) {
			time.Sleep(20 * time.Millisecond)
			done.Add(1)
		})
	}
	if err := tasks.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if done.Load() != 3 {
		t.Fatalf("Stop returned before the tasks finished: %d", done.Load())
	}
}

func TestTasksCancelsAtTheDeadline(t *testing.T) {
	tasks := NewTasks()
	canceled := make(chan struct{})
	tasks.Go(func(ctx context.Context) {
		<-ctx.Done()
		close(canceled)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := tasks.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	<-canceled
}

func TestTasksAfterStopRunInline(t *testing.T) {
	tasks := NewTasks()
	if err := tasks.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	ran := false
	tasks.Go(func(ctx context.Context) {
		ran = ctx.Err() != nil
	})
	if !ran {
		t.Fatal("a task spawned after Stop must run inline with a canceled context")
	}
}
