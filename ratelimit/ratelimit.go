// Package ratelimit 提供基于 Redis GCRA 漏桶算法的分布式限流组件
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereal3x/apc/cache"
	"github.com/go-redis/redis_rate/v10"
)

// Limiter 分布式限流器，基于 GCRA 漏桶算法
type Limiter struct {
	limiter *redis_rate.Limiter
}

// LimitConfig 单条限流规则配置
type LimitConfig struct {
	Rate   int           // 窗口内允许的请求数
	Period time.Duration // 统计窗口时长
	Burst  int           // 瞬间允许的突发请求数，通常等于 Rate
}

// LimitResult 限流检查结果
type LimitResult struct {
	Allowed    bool          // 是否允许通过
	Remaining  int           // 当前窗口剩余可用次数
	RetryAfter time.Duration // 被拒绝时的建议重试等待时长
}

// NewLimiter 基于 apc cache 的 Redis 客户端创建限流器
func NewLimiter(redis *cache.RedisClient) *Limiter {
	return &Limiter{
		limiter: redis_rate.NewLimiter(redis.UniversalClient()),
	}
}

// Allow 检查指定 key 是否允许通过，超限返回 Allowed=false
func (limiter *Limiter) Allow(ctx context.Context, key string, cfg LimitConfig) (*LimitResult, error) {
	limit := redis_rate.Limit{
		Rate:   cfg.Rate,
		Period: cfg.Period,
		Burst:  cfg.Burst,
	}
	result, err := limiter.limiter.Allow(ctx, key, limit)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: allow %q: %w", key, err)
	}
	return &LimitResult{
		Allowed:    result.Allowed > 0,
		Remaining:  result.Remaining,
		RetryAfter: result.RetryAfter,
	}, nil
}
