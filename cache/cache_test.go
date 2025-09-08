package cache

import (
	"context"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
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
}
