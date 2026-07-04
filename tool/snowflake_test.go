package tool

import (
	"sync"
	"testing"
)

// TestInitSnowflakeValid 校验合法 workerID 初始化成功
func TestInitSnowflakeValid(t *testing.T) {
	if err := InitSnowflake(1); err != nil {
		t.Fatalf("InitSnowflake(1) failed: %v", err)
	}
}

// TestInitSnowflakeInvalid 校验越界 workerID 返回错误
func TestInitSnowflakeInvalid(t *testing.T) {
	if err := InitSnowflake(-1); err == nil {
		t.Fatal("expected error for workerID -1")
	}
	if err := InitSnowflake(1024); err == nil {
		t.Fatal("expected error for workerID 1024")
	}
}

// TestGenSnowflakeIDUniqueness 校验单线程连续生成 ID 不重复
func TestGenSnowflakeIDUniqueness(t *testing.T) {
	_ = InitSnowflake(1)
	seen := make(map[int64]bool, 10000)
	for i := 0; i < 10000; i++ {
		id, err := GenSnowflakeID()
		if err != nil {
			t.Fatalf("GenSnowflakeID failed: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID %d at iteration %d", id, i)
		}
		seen[id] = true
	}
}

// TestGenSnowflakeIDConcurrent 校验并发生成不重复
func TestGenSnowflakeIDConcurrent(t *testing.T) {
	_ = InitSnowflake(2)
	var wg sync.WaitGroup
	const goroutines = 10
	const perGoroutine = 1000
	results := make([][]int64, goroutines)
	for i := 0; i < goroutines; i++ {
		results[i] = make([]int64, perGoroutine)
	}
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				id, err := GenSnowflakeID()
				if err != nil {
					t.Errorf("GenSnowflakeID failed: %v", err)
					return
				}
				results[idx][j] = id
			}
		}(i)
	}
	wg.Wait()
	seen := make(map[int64]bool, goroutines*perGoroutine)
	for _, batch := range results {
		for _, id := range batch {
			if seen[id] {
				t.Fatalf("duplicate ID %d in concurrent run", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != goroutines*perGoroutine {
		t.Fatalf("expected %d unique IDs, got %d", goroutines*perGoroutine, len(seen))
	}
}
