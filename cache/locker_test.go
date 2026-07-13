package cache

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// skipIfRedisUnavailable 在 Redis 不可用时跳过测试
func skipIfRedisUnavailable(t *testing.T, client *redis.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not available: %v", err)
	}
}

func TestRedisLock(t *testing.T) {
	// 初始化 Redis 客户端
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	skipIfRedisUnavailable(t, client)

	// 创建分布式锁实例
	lock := NewRedisLock(client, "test_lock", 3*time.Second)

	// 尝试获取锁并执行任务
	t.Run("Lock acquired successfully", func(t *testing.T) {
		err := lock.TryLock(context.Background(), func() error {
			// 执行需要分布式锁保护的操作
			fmt.Println("Lock acquired, performing task...")
			time.Sleep(2 * time.Second) // 模拟长时间任务
			fmt.Println("Task completed.")
			return nil
		})
		assert.Nil(t, err, "Should not return error when lock is acquired")
	})

	// 尝试获取锁并执行任务
	t.Run("Lock acquisition failed", func(t *testing.T) {
		// 获取另一个锁实例
		lock2 := NewRedisLock(client, "test_lock", 3*time.Second)
		err := lock2.TryLock(context.Background(), func() error {
			t.Errorf("Should not acquire lock when it's already held")
			return nil
		})
		assert.NotNil(t, err, "Should return error when lock acquisition fails")
	})

	// 手动释放锁
	t.Run("Lock released", func(t *testing.T) {
		err := lock.TryLock(context.Background(), func() error {
			// 执行需要分布式锁保护的操作
			fmt.Println("Lock acquired, performing task...")
			time.Sleep(1 * time.Second) // 模拟长时间任务
			fmt.Println("Task completed.")
			return nil
		})
		assert.Nil(t, err, "Should not return error when lock is acquired again")
	})
}

// TestRedisLockRunBusinessError 验证 Run 返回业务错误
func TestRedisLockRunBusinessError(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	skipIfRedisUnavailable(t, client)
	lock := NewRedisLock(client, "test_run_business_err", 3*time.Second)
	defer func() {
		_ = client.Del(context.Background(), "test_run_business_err").Err()
	}()

	expectedErr := errors.New("business failed")
	err := lock.Run(context.Background(), func(ctx context.Context) error {
		return expectedErr
	})

	assert.ErrorIs(t, err, expectedErr, "Run should return business error")
}

// TestRedisLockRunNotAcquired 验证获取锁失败时返回 ErrLockNotAcquired
func TestRedisLockRunNotAcquired(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	skipIfRedisUnavailable(t, client)
	holder := NewRedisLock(client, "test_run_not_acquired", 5*time.Second)
	contender := NewRedisLock(client, "test_run_not_acquired", 5*time.Second)
	defer func() {
		_ = client.Del(context.Background(), "test_run_not_acquired").Err()
	}()

	// 先持有锁
	holderCtx, holderCancel := context.WithCancel(context.Background())
	go func() {
		_ = holder.Run(holderCtx, func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
	}()

	// 等待持锁 goroutine 启动
	time.Sleep(100 * time.Millisecond)

	err := contender.Run(context.Background(), func(ctx context.Context) error {
		t.Fatal("should not execute fn when lock not acquired")
		return nil
	})

	assert.ErrorIs(t, err, ErrLockNotAcquired, "Run should return ErrLockNotAcquired")
	holderCancel()
	time.Sleep(100 * time.Millisecond)
}

// TestRedisLockRunCtxPassedThrough 验证 Run 将可取消的 context 传递给业务函数
func TestRedisLockRunCtxPassedThrough(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	skipIfRedisUnavailable(t, client)
	lock := NewRedisLock(client, "test_run_ctx", 3*time.Second)
	defer func() {
		_ = client.Del(context.Background(), "test_run_ctx").Err()
	}()

	parentCtx, parentCancel := context.WithCancel(context.Background())

	fnStarted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- lock.Run(parentCtx, func(ctx context.Context) error {
			close(fnStarted)
			<-ctx.Done()
			return ctx.Err()
		})
	}()

	<-fnStarted
	parentCancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled, "Run should return ctx cancellation error")
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after parent ctx cancel")
	}
}
