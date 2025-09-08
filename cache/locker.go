package cache

import (
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"time"
)

type RedisLock struct {
	client    *redis.Client
	lockName  string
	timeout   time.Duration
	ctx       context.Context
	keepAlive *time.Ticker // 用于定期续期
}

// NewRedisLock 创建一个新的 Redis 分布式锁
func NewRedisLock(client *redis.Client, lockName string, timeout time.Duration) *RedisLock {
	return &RedisLock{
		client:   client,
		lockName: lockName,
		timeout:  timeout,
		ctx:      context.Background(),
	}
}

// Acquire 尝试获取分布式锁
func (r *RedisLock) Acquire() (bool, error) {
	// 使用 Lua 脚本保证加锁操作的原子性
	luaScript := `
		if redis.call("SETNX", KEYS[1], ARGV[1]) == 1 then
			redis.call("PEXPIRE", KEYS[1], ARGV[2])
			return 1
		else
			return 0
		end
	`
	lockValue := "locked"
	ttl := int64(r.timeout / time.Millisecond)

	// 执行 Lua 脚本：如果锁不存在则设置，并且设置过期时间
	result, err := r.client.Eval(r.ctx, luaScript, []string{r.lockName}, lockValue, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("failed to acquire lock: %w", err)
	}

	if result.(int64) == 1 {
		return true, nil
	}
	return false, nil
}

// Release 释放分布式锁
func (r *RedisLock) Release() error {
	// 使用 Lua 脚本保证释放锁的原子性
	luaScript := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		else
			return 0
		end
	`

	lockValue := "locked"

	// 执行 Lua 脚本：只有值匹配时才删除锁
	_, err := r.client.Eval(r.ctx, luaScript, []string{r.lockName}, lockValue).Result()
	if err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}
	return nil
}

// KeepAlive 用于定期续期锁
func (r *RedisLock) KeepAlive() {
	// 设置定时器，每隔一段时间刷新锁
	r.keepAlive = time.NewTicker(r.timeout / 2)
	for {
		select {
		case <-r.keepAlive.C:
			// 使用 Lua 脚本续期
			luaScript := `
				if redis.call("GET", KEYS[1]) == ARGV[1] then
					redis.call("PEXPIRE", KEYS[1], ARGV[2])
					return 1
				else
					return 0
				end
			`
			lockValue := "locked"
			ttl := int64(r.timeout / time.Millisecond)

			_, err := r.client.Eval(r.ctx, luaScript, []string{r.lockName}, lockValue, ttl).Result()
			if err != nil {
				fmt.Printf("Failed to keep lock alive: %v\n", err)
				return
			}
		}
	}
}

// TryLock 尝试获取锁并执行某个操作
func (r *RedisLock) TryLock(fn func() error) error {
	// 尝试获取锁
	locked, err := r.Acquire()
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("could not acquire lock")
	}

	// 启动定时器进行锁续期
	go r.KeepAlive()

	// 确保锁在操作后释放
	defer func(r *RedisLock) {
		_ = r.Release()
	}(r)

	// 执行实际操作
	return fn()
}
