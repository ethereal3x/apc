package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/ethereal3x/apc/cache"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// newTestLimiter 创建用于测试的限流器实例
func newTestLimiter(t *testing.T) *Limiter {
	t.Helper()
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	redisClient := cache.NewRedisClient(client)
	return NewLimiter(redisClient)
}

func TestLimiterAllow(t *testing.T) {
	limiter := newTestLimiter(t)
	ctx := context.Background()

	// 测试正常通过：首次请求 Allowed=true，Remaining 正确递减
	t.Run("Allow first request passes", func(t *testing.T) {
		key := "ratelimit:allow:first"
		limiter.limiter.Reset(ctx, key)

		cfg := LimitConfig{
			Rate:   3,
			Period: time.Second,
			Burst:  3,
		}
		result, err := limiter.Allow(ctx, key, cfg)
		assert.Nil(t, err, "Should not return error on first allow")
		assert.True(t, result.Allowed, "First request should be allowed")
		assert.Equal(t, 2, result.Remaining, "Remaining should be 2 after first request")
	})

	// 测试超限拒绝：连续请求超过 Rate 后 Allowed=false，RetryAfter > 0
	t.Run("Allow denied after exceeding rate", func(t *testing.T) {
		key := "ratelimit:allow:denied"
		limiter.limiter.Reset(ctx, key)

		cfg := LimitConfig{
			Rate:   2,
			Period: time.Second,
			Burst:  2,
		}
		// 消耗全部配额
		for i := 0; i < 2; i++ {
			result, err := limiter.Allow(ctx, key, cfg)
			assert.Nil(t, err, "Should not return error while consuming quota")
			assert.True(t, result.Allowed, "Request within quota should be allowed")
		}
		// 第 3 次应被拒绝
		result, err := limiter.Allow(ctx, key, cfg)
		assert.Nil(t, err, "Should not return error when denied")
		assert.False(t, result.Allowed, "Request exceeding rate should be denied")
		assert.True(t, result.RetryAfter > 0, "RetryAfter should be positive when denied")
	})

	// 测试 Redis key 隔离：不同 key 互不影响
	t.Run("Allow different keys are isolated", func(t *testing.T) {
		keyA := "ratelimit:allow:isolated:a"
		keyB := "ratelimit:allow:isolated:b"
		limiter.limiter.Reset(ctx, keyA)
		limiter.limiter.Reset(ctx, keyB)

		cfg := LimitConfig{
			Rate:   1,
			Period: time.Second,
			Burst:  1,
		}
		// 消耗 keyA 的配额
		resultA, err := limiter.Allow(ctx, keyA, cfg)
		assert.Nil(t, err, "Should not return error on keyA first request")
		assert.True(t, resultA.Allowed, "keyA first request should be allowed")

		// keyA 第二次应被拒绝
		resultADenied, err := limiter.Allow(ctx, keyA, cfg)
		assert.Nil(t, err, "Should not return error on keyA second request")
		assert.False(t, resultADenied.Allowed, "keyA second request should be denied")

		// keyB 不受影响，仍可通过
		resultB, err := limiter.Allow(ctx, keyB, cfg)
		assert.Nil(t, err, "Should not return error on keyB request")
		assert.True(t, resultB.Allowed, "keyB should not be affected by keyA")
	})
}

func TestRuleGroupCheck(t *testing.T) {
	limiter := newTestLimiter(t)
	ctx := context.Background()

	// 测试多规则全部通过返回空
	t.Run("All rules pass returns empty", func(t *testing.T) {
		key1 := "ratelimit:group:allpass:1"
		key2 := "ratelimit:group:allpass:2"
		limiter.limiter.Reset(ctx, key1)
		limiter.limiter.Reset(ctx, key2)

		rules := []Rule{
			{
				Name: "rule_one",
				Key:  func(_ context.Context) string { return key1 },
				Config: LimitConfig{
					Rate:   10,
					Period: time.Second,
					Burst:  10,
				},
			},
			{
				Name: "rule_two",
				Key:  func(_ context.Context) string { return key2 },
				Config: LimitConfig{
					Rate:   10,
					Period: time.Second,
					Burst:  10,
				},
			},
		}
		group := NewRuleGroup(limiter, rules)
		deniedRule, err := group.Check(ctx)
		assert.Nil(t, err, "Should not return error when all rules pass")
		assert.Equal(t, "", deniedRule, "Denied rule should be empty when all pass")
	})

	// 测试首个拒绝规则名正确返回
	t.Run("First denied rule name returned", func(t *testing.T) {
		key1 := "ratelimit:group:denied:1"
		key2 := "ratelimit:group:denied:2"
		limiter.limiter.Reset(ctx, key1)
		limiter.limiter.Reset(ctx, key2)

		// rule_one 配额为 1，先消耗掉
		cfgOne := LimitConfig{
			Rate:   1,
			Period: time.Second,
			Burst:  1,
		}
		_, err := limiter.Allow(ctx, key1, cfgOne)
		assert.Nil(t, err, "Should not return error while consuming rule_one quota")

		rules := []Rule{
			{
				Name:   "rule_one",
				Key:    func(_ context.Context) string { return key1 },
				Config: cfgOne,
			},
			{
				Name: "rule_two",
				Key:  func(_ context.Context) string { return key2 },
				Config: LimitConfig{
					Rate:   10,
					Period: time.Second,
					Burst:  10,
				},
			},
		}
		group := NewRuleGroup(limiter, rules)
		deniedRule, err := group.Check(ctx)
		assert.Nil(t, err, "Should not return error when a rule denies")
		assert.Equal(t, "rule_one", deniedRule, "Should return the first denied rule name")
	})

	// 测试 Key 回调返回空字符串时跳过该规则
	t.Run("Rule with empty key is skipped", func(t *testing.T) {
		key2 := "ratelimit:group:skip:2"
		limiter.limiter.Reset(ctx, key2)

		rules := []Rule{
			{
				Name: "rule_skip",
				Key:  func(_ context.Context) string { return "" },
				Config: LimitConfig{
					Rate:   1,
					Period: time.Second,
					Burst:  1,
				},
			},
			{
				Name: "rule_two",
				Key:  func(_ context.Context) string { return key2 },
				Config: LimitConfig{
					Rate:   10,
					Period: time.Second,
					Burst:  10,
				},
			},
		}
		group := NewRuleGroup(limiter, rules)
		deniedRule, err := group.Check(ctx)
		assert.Nil(t, err, "Should not return error when skipped rule passes")
		assert.Equal(t, "", deniedRule, "Should return empty when skipped rule is not checked")
	})
}
