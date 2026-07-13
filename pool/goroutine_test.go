package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutorSubmitAndWait(t *testing.T) {
	executor := NewExecutor(3, 8)
	defer executor.Close()

	var sum atomic.Int64
	for i := 1; i <= 100; i++ {
		n := int64(i)
		if err := executor.Submit(context.Background(), func() {
			sum.Add(n)
		}); err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
	}

	executor.Wait()
	if sum.Load() != 5050 {
		t.Fatalf("sum = %d, want 5050", sum.Load())
	}
}

func TestExecutorClose(t *testing.T) {
	executor := NewExecutor(1, 1)
	if err := executor.Submit(context.Background(), func() {}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	executor.Wait()
	executor.Close()
	executor.Close()

	if err := executor.Submit(context.Background(), func() {}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit() error = %v, want %v", err, ErrClosed)
	}
}

func TestExecutorNilTask(t *testing.T) {
	executor := NewExecutor(1)
	defer executor.Close()

	if err := executor.Submit(context.Background(), nil); !errors.Is(err, ErrNilTask) {
		t.Fatalf("Submit(nil) error = %v, want %v", err, ErrNilTask)
	}
}

func TestExecutorDefaultWorker(t *testing.T) {
	executor := NewExecutor(0)
	defer executor.Close()

	done := make(chan struct{})
	if err := executor.Submit(context.Background(), func() {
		close(done)
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	executor.Wait()

	select {
	case <-done:
	default:
		t.Fatal("task did not run")
	}
}

// TestExecutorPanicIsolation 验证任务 panic 不会跳过 Done，Wait 不会永久阻塞
func TestExecutorPanicIsolation(t *testing.T) {
	executor := NewExecutor(2, 4)
	defer executor.Close()

	for range 5 {
		if err := executor.Submit(context.Background(), func() {
			panic("boom")
		}); err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		executor.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() blocked after task panic, Done was skipped")
	}
}

// TestExecutorSubmitCtxCancel 验证 Submit 在 ctx 取消时立即返回 ctx.Err()
func TestExecutorSubmitCtxCancel(t *testing.T) {
	executor := NewExecutor(1, 0)
	defer executor.Close()

	// 占满唯一的 worker，使后续 Submit 阻塞
	block := make(chan struct{})
	if err := executor.Submit(context.Background(), func() {
		<-block
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := executor.Submit(ctx, func() {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Submit() error = %v, want %v", err, context.DeadlineExceeded)
	}

	close(block)
	executor.Wait()
}

// TestExecutorCloseWaitsQueuedTasks 验证 Close 等待队列中剩余任务执行完毕
func TestExecutorCloseWaitsQueuedTasks(t *testing.T) {
	executor := NewExecutor(1, 4)

	var executed atomic.Int64
	for range 3 {
		if err := executor.Submit(context.Background(), func() {
			time.Sleep(20 * time.Millisecond)
			executed.Add(1)
		}); err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
	}

	executor.Close()
	if executed.Load() != 3 {
		t.Fatalf("executed = %d, want 3 after Close", executed.Load())
	}
}

// TestExecutorConcurrentSubmitAndClose 验证并发 Submit 与 Close 不触发 panic
func TestExecutorConcurrentSubmitAndClose(t *testing.T) {
	executor := NewExecutor(2, 2)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = executor.Submit(context.Background(), func() {})
		}()
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		executor.Close()
	}()

	wg.Wait()
}
