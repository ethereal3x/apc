package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestRedisClient(t *testing.T) {
	// 初始化 Redis 客户端
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 创建 RedisClient 实例
	redisClient := NewRedisClient(context.Background(), client)

	// 测试 Set 和 Get
	t.Run("Test Set and Get", func(t *testing.T) {
		key := "test_key"
		value := "test_value"

		// 设置值
		err := redisClient.Set(key, value, 10*time.Second)
		assert.Nil(t, err, "Should not return error while setting value")

		// 获取值
		got, err := redisClient.Get(key)
		assert.Nil(t, err, "Should not return error while getting value")
		assert.Equal(t, value, got, "The value should match")
	})

	// 测试 Del
	t.Run("Test Del", func(t *testing.T) {
		key := "test_del_key"
		value := "to_be_deleted"

		// 设置值
		err := redisClient.Set(key, value, 10*time.Second)
		assert.Nil(t, err, "Should not return error while setting value")

		// 删除值
		err = redisClient.Del(key)
		assert.Nil(t, err, "Should not return error while deleting value")

		// 尝试获取已删除的值
		got, err := redisClient.Get(key)
		assert.Nil(t, err, "Should not return error while getting deleted value")
		assert.Empty(t, got, "The value should be empty after deletion")
	})

	// 测试 MSet 和 MGet
	t.Run("Test MSet and MGet", func(t *testing.T) {
		values := []interface{}{"key1", "value1", "key2", "value2"}

		// 批量设置多个值
		err := redisClient.MSet(values...)
		assert.Nil(t, err, "Should not return error while setting multiple values")

		// 批量获取多个值
		got, err := redisClient.MGet("key1", "key2")
		assert.Nil(t, err, "Should not return error while getting multiple values")
		assert.Equal(t, []interface{}{"value1", "value2"}, got, "The values should match")
	})

	// 测试 Expire 和 TTL
	t.Run("Test Expire and TTL", func(t *testing.T) {
		key := "test_ttl_key"
		value := "value_with_ttl"

		// 设置带 TTL 的值
		err := redisClient.Set(key, value, 2*time.Second)
		assert.Nil(t, err, "Should not return error while setting value with ttl")

		// 获取 TTL
		ttl, err := redisClient.TTL(key)
		assert.Nil(t, err, "Should not return error while getting ttl")
		assert.True(t, ttl <= 2*time.Second, "TTL should be less than or equal to 2 seconds")

		// 等待 TTL 到期
		time.Sleep(3 * time.Second)

		// 获取值，应该为空
		got, err := redisClient.Get(key)
		assert.Nil(t, err, "Should not return error while getting expired value")
		assert.Empty(t, got, "The value should be empty after TTL expires")
	})

	// 测试 Exists
	t.Run("Test Exists", func(t *testing.T) {
		key := "test_exists_key"
		value := "value_to_exist"

		// 设置值
		err := redisClient.Set(key, value, 10*time.Second)
		assert.Nil(t, err, "Should not return error while setting value")

		// 检查 key 是否存在
		count, err := redisClient.Exists(key)
		assert.Nil(t, err, "Should not return error while checking key existence")
		assert.Equal(t, int64(1), count, "The key should exist")
	})

	// 测试 Incr 和 Decr
	t.Run("Test Incr and Decr", func(t *testing.T) {
		key := "test_incr_decr_key"
		// 设置初始值
		err := redisClient.Set(key, 0, 0)
		assert.Nil(t, err, "Should not return error while setting initial value")

		// 自增
		incrResult, err := redisClient.Incr(key)
		assert.Nil(t, err, "Should not return error while incrementing value")
		assert.Equal(t, int64(1), incrResult, "The value should be incremented by 1")

		// 自减
		decrResult, err := redisClient.Decr(key)
		assert.Nil(t, err, "Should not return error while decrementing value")
		assert.Equal(t, int64(0), decrResult, "The value should be decremented by 1")
	})

	// 测试 HSet 和 HGet
	t.Run("Test HSet and HGet", func(t *testing.T) {
		hashKey := "test_hash_key"
		field := "field1"
		value := "hash_value"

		// 设置哈希表中的字段值
		err := redisClient.HSet(hashKey, field, value)
		assert.Nil(t, err, "Should not return error while setting hash field")

		// 获取哈希表中的字段值
		got, err := redisClient.HGet(hashKey, field)
		assert.Nil(t, err, "Should not return error while getting hash field")
		assert.Equal(t, value, got, "The hash field value should match")
	})

	// 测试 SAdd 和 SMembers
	t.Run("Test SAdd and SMembers", func(t *testing.T) {
		setKey := "test_set_key"
		members := []interface{}{"member1", "member2", "member3"}

		// 向集合中添加元素
		count, err := redisClient.SAdd(setKey, members...)
		assert.Nil(t, err, "Should not return error while adding members to set")
		assert.Equal(t, int64(3), count, "The number of added members should match")

		// 获取集合中的所有元素
		got, err := redisClient.SMembers(setKey)
		assert.Nil(t, err, "Should not return error while getting set members")
		assert.ElementsMatch(t, members, got, "The set members should match")
	})

	// 测试存储十万条用户 ID 到集合中 -- 0.055s
	t.Run("Test Store 10000 User IDs", func(t *testing.T) {
		setKey := "user_ids_set"
		startID := 1000
		count := 100000
		ttl := 30 * 24 * time.Hour // 30 天

		// 生成用户 ID 列表（从 1000 到 10999）
		userIDs := make([]interface{}, count)
		for i := 0; i < count; i++ {
			userIDs[i] = startID + i
		}

		// 批量添加用户 ID 到集合
		addedCount, err := redisClient.SAdd(setKey, userIDs...)
		assert.Nil(t, err, "Should not return error while adding user IDs to set")
		assert.Equal(t, int64(count), addedCount, "The number of added user IDs should be 10000")

		// 设置过期时间为 30 天
		err = redisClient.Expire(setKey, ttl)
		assert.Nil(t, err, "Should not return error while setting expiration")

		// 验证过期时间是否设置成功
		remainingTTL, err := redisClient.TTL(setKey)
		assert.Nil(t, err, "Should not return error while getting TTL")
		assert.True(t, remainingTTL > 0 && remainingTTL <= ttl, "TTL should be set correctly")

		// 验证集合中的元素数量
		members, err := redisClient.SMembers(setKey)
		assert.Nil(t, err, "Should not return error while getting set members")
		assert.Equal(t, count, len(members), "The set should contain 10000 members")
	})

	// 测试从十万条数据中查询某个 ID 是否存在
	/**
	cache_test.go:223: 查询存在的 ID (1000) 耗时: 145.5µs
	cache_test.go:232: 查询存在的 ID (51000) 耗时: 134.458µs
	cache_test.go:241: 查询存在的 ID (100999) 耗时: 125.834µs
	cache_test.go:250: 查询不存在的 ID (102000) 耗时: 119.959µs
	cache_test.go:262: 批量查询 1000 次总耗时: 82.531208ms, 平均每次: 82.531µs
	*/
	t.Run("Test Query User ID Existence Speed", func(t *testing.T) {
		setKey := "user_ids_query_test"
		startID := 1000
		count := 100000
		ttl := 30 * 24 * time.Hour // 30 天

		// 生成用户 ID 列表（从 1000 到 100999）
		userIDs := make([]interface{}, count)
		for i := 0; i < count; i++ {
			userIDs[i] = startID + i
		}

		// 批量添加用户 ID 到集合
		_, err := redisClient.SAdd(setKey, userIDs...)
		assert.Nil(t, err, "Should not return error while adding user IDs to set")

		// 设置过期时间
		err = redisClient.Expire(setKey, ttl)
		assert.Nil(t, err, "Should not return error while setting expiration")

		// 验证集合大小
		cardCount, err := redisClient.SCard(setKey)
		assert.Nil(t, err, "Should not return error while getting set card")
		assert.Equal(t, int64(count), cardCount, "The set should contain 100000 members")

		// 测试查询存在的 ID（第一个）
		testID1 := startID
		start1 := time.Now()
		exists1, err := redisClient.SIsMember(setKey, testID1)
		duration1 := time.Since(start1)
		assert.Nil(t, err, "Should not return error while checking member existence")
		assert.True(t, exists1, "The user ID should exist in the set")
		t.Logf("查询存在的 ID (%d) 耗时: %v", testID1, duration1)

		// 测试查询存在的 ID（中间）
		testID2 := startID + count/2
		start2 := time.Now()
		exists2, err := redisClient.SIsMember(setKey, testID2)
		duration2 := time.Since(start2)
		assert.Nil(t, err, "Should not return error while checking member existence")
		assert.True(t, exists2, "The user ID should exist in the set")
		t.Logf("查询存在的 ID (%d) 耗时: %v", testID2, duration2)

		// 测试查询存在的 ID（最后一个）
		testID3 := startID + count - 1
		start3 := time.Now()
		exists3, err := redisClient.SIsMember(setKey, testID3)
		duration3 := time.Since(start3)
		assert.Nil(t, err, "Should not return error while checking member existence")
		assert.True(t, exists3, "The user ID should exist in the set")
		t.Logf("查询存在的 ID (%d) 耗时: %v", testID3, duration3)

		// 测试查询不存在的 ID
		testID4 := startID + count + 1000
		start4 := time.Now()
		exists4, err := redisClient.SIsMember(setKey, testID4)
		duration4 := time.Since(start4)
		assert.Nil(t, err, "Should not return error while checking member existence")
		assert.False(t, exists4, "The user ID should not exist in the set")
		t.Logf("查询不存在的 ID (%d) 耗时: %v", testID4, duration4)

		// 批量查询测试（连续查询 1000 次）
		batchCount := 1000
		batchStart := time.Now()
		for i := 0; i < batchCount; i++ {
			testID := startID + (i * count / batchCount)
			_, err := redisClient.SIsMember(setKey, testID)
			assert.Nil(t, err, "Should not return error in batch query")
		}
		batchDuration := time.Since(batchStart)
		avgDuration := batchDuration / time.Duration(batchCount)
		t.Logf("批量查询 %d 次总耗时: %v, 平均每次: %v", batchCount, batchDuration, avgDuration)

		// 清理测试数据
		err = redisClient.Del(setKey)
		assert.Nil(t, err, "Should not return error while cleaning up test data")
	})
}
