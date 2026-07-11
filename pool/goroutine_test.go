package pool

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestGoroutinePoolSubmitAndWait(t *testing.T) {
	p := NewGoroutinePool(3, 8)
	defer p.Close()

	var sum atomic.Int64
	for i := 1; i <= 100; i++ {
		n := int64(i)
		if err := p.Submit(func() {
			sum.Add(n)
		}); err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
	}

	p.Wait()
	if sum.Load() != 5050 {
		t.Fatalf("sum = %d, want 5050", sum.Load())
	}
}

func TestGoroutinePoolClose(t *testing.T) {
	p := NewGoroutinePool(1, 1)
	if err := p.Submit(func() {}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	p.Wait()
	p.Close()
	p.Close()

	if err := p.Submit(func() {}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit() error = %v, want %v", err, ErrClosed)
	}
}

func TestGoroutinePoolNilTask(t *testing.T) {
	p := NewGoroutinePool(1)
	defer p.Close()

	if err := p.Submit(nil); !errors.Is(err, ErrNilTask) {
		t.Fatalf("Submit(nil) error = %v, want %v", err, ErrNilTask)
	}
}

func TestGoroutinePoolDefaultWorker(t *testing.T) {
	p := NewGoroutinePool(0)
	defer p.Close()

	done := make(chan struct{})
	if err := p.Submit(func() {
		close(done)
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	p.Wait()

	select {
	case <-done:
	default:
		t.Fatal("task did not run")
	}
}
