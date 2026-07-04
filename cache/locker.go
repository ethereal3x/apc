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

// RedisLock 基于 Redis 的分布式锁实现
type RedisLock struct {
	client      *redis.Client
	lockName    string
	lockValue   string
	timeout     time.Duration
	mu          sync.Mutex
	keepAliveCh chan struct{}
	keepAlive   bool
}

// NewRedisLock 创建 Redis 分布式锁实例，lockValue 自动生成唯一标识防止误释放
func NewRedisLock(client *redis.Client, lockName string, timeout time.Duration) *RedisLock {
	return &RedisLock{
		client:    client,
		lockName:  lockName,
		lockValue: uuid.New().String(),
		timeout:   timeout,
	}
}

// Acquire 尝试获取分布式锁，通过 context 控制超时
func (r *RedisLock) Acquire(ctx context.Context) (bool, error) {
	luaScript := `
		if redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2], "NX") then
			return 1
		else
			return 0
		end
	`
	ttl := int64(r.timeout / time.Millisecond)
	result, err := r.client.Eval(ctx, luaScript, []string{r.lockName}, r.lockValue, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cache: acquire lock %q: %w", r.lockName, err)
	}
	return result.(int64) == 1, nil
}

// Release 释放分布式锁，仅当锁值匹配时才删除
func (r *RedisLock) Release(ctx context.Context) error {
	r.stopKeepAlive()
	luaScript := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		else
			return 0
		end
	`
	_, err := r.client.Eval(ctx, luaScript, []string{r.lockName}, r.lockValue).Result()
	if err != nil {
		return fmt.Errorf("cache: release lock %q: %w", r.lockName, err)
	}
	return nil
}

// KeepAlive 启动定期续期 goroutine，重复调用不重复启动
func (r *RedisLock) KeepAlive() {
	r.mu.Lock()
	if r.keepAlive {
		r.mu.Unlock()
		return
	}
	r.keepAlive = true
	r.keepAliveCh = make(chan struct{})
	ch := r.keepAliveCh
	r.mu.Unlock()

	ticker := time.NewTicker(r.timeout / 2)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.renew()
			case <-ch:
				return
			}
		}
	}()
}

// stopKeepAlive 停止续期 goroutine
func (r *RedisLock) stopKeepAlive() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.keepAlive {
		return
	}
	close(r.keepAliveCh)
	r.keepAlive = false
}

// renew 续期锁的过期时间
func (r *RedisLock) renew() {
	luaScript := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("PEXPIRE", KEYS[1], ARGV[2])
		else
			return 0
		end
	`
	ttl := int64(r.timeout / time.Millisecond)
	_, err := r.client.Eval(context.Background(), luaScript, []string{r.lockName}, r.lockValue, ttl).Result()
	if err != nil {
		fmt.Printf("cache: keep lock %q alive failed: %v\n", r.lockName, err)
	}
}

// TryLock 尝试获取锁并执行操作，自动处理续期和释放
func (r *RedisLock) TryLock(ctx context.Context, fn func() error) error {
	locked, err := r.Acquire(ctx)
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("cache: could not acquire lock")
	}
	r.KeepAlive()
	defer func() {
		_ = r.Release(context.Background())
	}()
	return fn()
}
