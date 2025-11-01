package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisClient struct {
	ctx    context.Context
	client *redis.Client
}

func NewRedisClient(ctx context.Context, client *redis.Client) *RedisClient {
	return &RedisClient{
		ctx:    ctx,
		client: client,
	}
}

// Get 获取单个key的值
func (r *RedisClient) Get(key string) (string, error) {
	val, err := r.client.Get(r.ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil // 业务层自己判断空值
	}
	if err != nil {
		return "", fmt.Errorf("cache: get %q: %w", key, err)
	}
	return val, nil
}

// Set 设置单个key的值
func (r *RedisClient) Set(key string, val any, ttl time.Duration) error {
	if err := r.client.Set(r.ctx, key, val, ttl).Err(); err != nil {
		return fmt.Errorf("cache: set %q: %w", key, err)
	}
	return nil
}

// Del 删除指定的key
func (r *RedisClient) Del(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := r.client.Del(r.ctx, keys...).Err(); err != nil {
		return fmt.Errorf("cache: del %v: %w", keys, err)
	}
	return nil
}

// MGet 批量获取多个key的值
func (r *RedisClient) MGet(keys ...string) ([]interface{}, error) {
	if len(keys) == 0 {
		return []interface{}{}, nil
	}
	result, err := r.client.MGet(r.ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: mget %v: %w", keys, err)
	}
	return result, nil
}

// MSet 批量设置多个key-value对
func (r *RedisClient) MSet(values ...interface{}) error {
	if len(values) == 0 || len(values)%2 != 0 {
		return errors.New("cache: mset requires even number of arguments")
	}
	if err := r.client.MSet(r.ctx, values...).Err(); err != nil {
		return fmt.Errorf("cache: mset: %w", err)
	}
	return nil
}

// Expire 设置key的过期时间
func (r *RedisClient) Expire(key string, ttl time.Duration) error {
	if err := r.client.Expire(r.ctx, key, ttl).Err(); err != nil {
		return fmt.Errorf("cache: expire %q: %w", key, err)
	}
	return nil
}

// TTL 获取key的剩余过期时间
func (r *RedisClient) TTL(key string) (time.Duration, error) {
	ttl, err := r.client.TTL(r.ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: ttl %q: %w", key, err)
	}
	return ttl, nil
}

// Exists 检查key是否存在
func (r *RedisClient) Exists(keys ...string) (int64, error) {
	count, err := r.client.Exists(r.ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: exists %v: %w", keys, err)
	}
	return count, nil
}

// HGet 获取哈希表中的字段值
func (r *RedisClient) HGet(key, field string) (string, error) {
	val, err := r.client.HGet(r.ctx, key, field).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: hget %q:%q: %w", key, field, err)
	}
	return val, nil
}

// HSet 设置哈希表中的字段值
func (r *RedisClient) HSet(key string, values ...interface{}) error {
	if len(values) == 0 || len(values)%2 != 0 {
		return errors.New("cache: hset requires even number of arguments")
	}
	if err := r.client.HSet(r.ctx, key, values...).Err(); err != nil {
		return fmt.Errorf("cache: hset %q: %w", key, err)
	}
	return nil
}

// HGetAll 获取哈希表中所有的字段和值
func (r *RedisClient) HGetAll(key string) (map[string]string, error) {
	result, err := r.client.HGetAll(r.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: hgetall %q: %w", key, err)
	}
	return result, nil
}

// Incr 对key的值进行自增操作
func (r *RedisClient) Incr(key string) (int64, error) {
	val, err := r.client.Incr(r.ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: incr %q: %w", key, err)
	}
	return val, nil
}

// Decr 对key的值进行自减操作
func (r *RedisClient) Decr(key string) (int64, error) {
	val, err := r.client.Decr(r.ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: decr %q: %w", key, err)
	}
	return val, nil
}

// IncrBy 对key的值进行指定步长的自增操作
func (r *RedisClient) IncrBy(key string, step int64) (int64, error) {
	val, err := r.client.IncrBy(r.ctx, key, step).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: incrby %q: %w", key, err)
	}
	return val, nil
}

// DecrBy 对key的值进行指定步长的自减操作
func (r *RedisClient) DecrBy(key string, step int64) (int64, error) {
	val, err := r.client.DecrBy(r.ctx, key, step).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: decrby %q: %w", key, err)
	}
	return val, nil
}

// SAdd 向集合中添加元素
func (r *RedisClient) SAdd(key string, members ...interface{}) (int64, error) {
	count, err := r.client.SAdd(r.ctx, key, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: sadd %q: %w", key, err)
	}
	return count, nil
}

// SMembers 获取集合中的所有元素
func (r *RedisClient) SMembers(key string) ([]string, error) {
	members, err := r.client.SMembers(r.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: smembers %q: %w", key, err)
	}
	return members, nil
}

// SIsMember 检查元素是否在集合中
func (r *RedisClient) SIsMember(key string, member interface{}) (bool, error) {
	exists, err := r.client.SIsMember(r.ctx, key, member).Result()
	if err != nil {
		return false, fmt.Errorf("cache: sismember %q: %w", key, err)
	}
	return exists, nil
}

// SCard 获取集合中元素的数量
func (r *RedisClient) SCard(key string) (int64, error) {
	count, err := r.client.SCard(r.ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: scard %q: %w", key, err)
	}
	return count, nil
}
