package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const defaultRedisLockTimeout = 30 * time.Second

// ErrLockNotAcquired 获取锁失败
var ErrLockNotAcquired = errors.New("cache: could not acquire lock")

// ErrLockLost 续租时发现锁已丢失
var ErrLockLost = errors.New("cache: lock lost during renewal")

// RedisLock 基于 Redis 的分布式锁实现，提供 Lease 模型的 Run 方法
type RedisLock struct {
	client    *redis.Client
	lockName  string
	lockValue string
	timeout   time.Duration

	mu          sync.Mutex
	keepAlive   bool
	keepAliveCh chan struct{}
}

// NewRedisLock 创建 Redis 分布式锁实例，lockValue 自动生成唯一标识防止误释放
func NewRedisLock(client *redis.Client, lockName string, timeout time.Duration) *RedisLock {
	if timeout <= 0 {
		timeout = defaultRedisLockTimeout
	}
	return &RedisLock{
		client:    client,
		lockName:  lockName,
		lockValue: uuid.New().String(),
		timeout:   timeout,
	}
}

// Acquire 尝试获取分布式锁，通过 context 控制超时
func (lock *RedisLock) Acquire(ctx context.Context) (bool, error) {
	luaScript := `
		if redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2], "NX") then
			return 1
		else
			return 0
		end
	`
	ttl := int64(lock.timeout / time.Millisecond)
	result, err := lock.client.Eval(ctx, luaScript, []string{lock.lockName}, lock.lockValue, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cache: acquire lock %q: %w", lock.lockName, err)
	}
	return result.(int64) == 1, nil
}

// Release 释放分布式锁，仅当锁值匹配时才删除
func (lock *RedisLock) Release(ctx context.Context) error {
	lock.stopKeepAlive()
	locked, err := lock.release(ctx)
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("cache: release lock %q: lock already lost or value mismatch", lock.lockName)
	}
	return nil
}

// KeepAlive 启动定期续期 goroutine，重复调用不重复启动
func (lock *RedisLock) KeepAlive() {
	lock.mu.Lock()
	if lock.keepAlive {
		lock.mu.Unlock()
		return
	}
	lock.keepAlive = true
	lock.keepAliveCh = make(chan struct{})
	stopCh := lock.keepAliveCh
	lock.mu.Unlock()

	ticker := time.NewTicker(lock.timeout / 2)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := lock.renew(context.Background()); err != nil {
					fmt.Printf("cache: keep lock %q alive failed: %v\n", lock.lockName, err)
				}
			case <-stopCh:
				return
			}
		}
	}()
}

// stopKeepAlive 停止续期 goroutine
func (lock *RedisLock) stopKeepAlive() {
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if !lock.keepAlive {
		return
	}
	close(lock.keepAliveCh)
	lock.keepAlive = false
}

// renew 续期锁的过期时间并返回是否仍持有锁
func (lock *RedisLock) renew(ctx context.Context) (bool, error) {
	luaScript := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("PEXPIRE", KEYS[1], ARGV[2])
		else
			return 0
		end
	`
	ttl := int64(lock.timeout / time.Millisecond)
	result, err := lock.client.Eval(ctx, luaScript, []string{lock.lockName}, lock.lockValue, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cache: renew lock %q: %w", lock.lockName, err)
	}
	return result.(int64) == 1, nil
}

// release 执行 Redis 原子释放脚本并返回锁是否成功删除
func (lock *RedisLock) release(ctx context.Context) (bool, error) {
	luaScript := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		else
			return 0
		end
	`
	result, err := lock.client.Eval(ctx, luaScript, []string{lock.lockName}, lock.lockValue).Result()
	if err != nil {
		return false, fmt.Errorf("cache: release lock %q: %w", lock.lockName, err)
	}
	return result.(int64) == 1, nil
}

// Run 以 Lease 模型执行业务：获取锁、启动续租、执行业务、释放锁
// 续租失败或锁丢失时取消业务 context，业务错误和释放错误均会返回
func (lock *RedisLock) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	locked, err := lock.Acquire(ctx)
	if err != nil {
		return err
	}
	if !locked {
		return ErrLockNotAcquired
	}

	leaseCtx, leaseCancel := context.WithCancel(ctx)
	defer leaseCancel()

	// 启动租约续期并在锁丢失时取消业务
	renewErrCh, err := lock.startRenewal(leaseCtx, leaseCancel)
	if err != nil {
		return errors.Join(err, lock.releaseWithTimeout(ctx))
	}

	// 执行业务函数并传入可取消租约 context
	businessErr := fn(leaseCtx)

	// 停止续租并收集续租结果
	lock.stopKeepAlive()
	renewErr := <-renewErrCh

	// 使用不继承取消状态的 context 释放锁
	releaseErr := lock.releaseWithTimeout(ctx)

	return errors.Join(businessErr, renewErr, releaseErr)
}

// startRenewal 启动续租 goroutine，续租失败时取消业务 context 并通过 channel 返回错误
func (lock *RedisLock) startRenewal(ctx context.Context, cancel context.CancelFunc) (<-chan error, error) {
	errCh := make(chan error, 1)

	lock.mu.Lock()
	if lock.keepAlive {
		lock.mu.Unlock()
		close(errCh)
		return errCh, fmt.Errorf("cache: lock %q renewal already running", lock.lockName)
	}
	lock.keepAlive = true
	lock.keepAliveCh = make(chan struct{})
	stopCh := lock.keepAliveCh
	lock.mu.Unlock()

	go func() {
		defer close(errCh)
		ticker := time.NewTicker(lock.timeout / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				locked, err := lock.renew(ctx)
				if err != nil {
					errCh <- err
					cancel()
					return
				}
				if !locked {
					errCh <- ErrLockLost
					cancel()
					return
				}
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return errCh, nil
}

// releaseWithTimeout 使用独立超时 context 释放锁，避免父 context 已取消导致释放失败
func (lock *RedisLock) releaseWithTimeout(ctx context.Context) error {
	releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer releaseCancel()
	return lock.Release(releaseCtx)
}

// TryLock 尝试获取锁并执行操作，自动处理续期和释放
// 保留向后兼容，新代码应使用 Run 方法获取完整的错误信息
func (lock *RedisLock) TryLock(ctx context.Context, fn func() error) error {
	return lock.Run(ctx, func(context.Context) error {
		return fn()
	})
}
