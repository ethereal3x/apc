package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisClient struct {
	client *redis.Client
}

func NewRedisClient(client *redis.Client) *RedisClient {
	return &RedisClient{client: client}
}

// Get 获取单个key的值
func (r *RedisClient) Get(ctx context.Context, key string) (string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil // 业务层自己判断空值
	}
	if err != nil {
		return "", fmt.Errorf("cache: get %q: %w", key, err)
	}
	return val, nil
}

// Set 设置单个key的值
func (r *RedisClient) Set(ctx context.Context, key string, val any, ttl time.Duration) error {
	if err := r.client.Set(ctx, key, val, ttl).Err(); err != nil {
		return fmt.Errorf("cache: set %q: %w", key, err)
	}
	return nil
}

// Del 删除指定的key
func (r *RedisClient) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := r.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("cache: del %v: %w", keys, err)
	}
	return nil
}

// MGet 批量获取多个key的值
func (r *RedisClient) MGet(ctx context.Context, keys ...string) ([]interface{}, error) {
	if len(keys) == 0 {
		return []interface{}{}, nil
	}
	result, err := r.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: mget %v: %w", keys, err)
	}
	return result, nil
}

// MSet 批量设置多个key-value对
func (r *RedisClient) MSet(ctx context.Context, values ...interface{}) error {
	if len(values) == 0 || len(values)%2 != 0 {
		return errors.New("cache: mset requires even number of arguments")
	}
	if err := r.client.MSet(ctx, values...).Err(); err != nil {
		return fmt.Errorf("cache: mset: %w", err)
	}
	return nil
}

// Expire 设置key的过期时间
func (r *RedisClient) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := r.client.Expire(ctx, key, ttl).Err(); err != nil {
		return fmt.Errorf("cache: expire %q: %w", key, err)
	}
	return nil
}

// TTL 获取key的剩余过期时间
func (r *RedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	ttl, err := r.client.TTL(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: ttl %q: %w", key, err)
	}
	return ttl, nil
}

// Exists 检查key是否存在
func (r *RedisClient) Exists(ctx context.Context, keys ...string) (int64, error) {
	count, err := r.client.Exists(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: exists %v: %w", keys, err)
	}
	return count, nil
}

// HGet 获取哈希表中的字段值
func (r *RedisClient) HGet(ctx context.Context, key, field string) (string, error) {
	val, err := r.client.HGet(ctx, key, field).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: hget %q:%q: %w", key, field, err)
	}
	return val, nil
}

// HSet 设置哈希表中的字段值
func (r *RedisClient) HSet(ctx context.Context, key string, values ...interface{}) error {
	if len(values) == 0 || len(values)%2 != 0 {
		return errors.New("cache: hset requires even number of arguments")
	}
	if err := r.client.HSet(ctx, key, values...).Err(); err != nil {
		return fmt.Errorf("cache: hset %q: %w", key, err)
	}
	return nil
}

// HGetAll 获取哈希表中所有的字段和值
func (r *RedisClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	result, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: hgetall %q: %w", key, err)
	}
	return result, nil
}

// Incr 对key的值进行自增操作
func (r *RedisClient) Incr(ctx context.Context, key string) (int64, error) {
	val, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: incr %q: %w", key, err)
	}
	return val, nil
}

// Decr 对key的值进行自减操作
func (r *RedisClient) Decr(ctx context.Context, key string) (int64, error) {
	val, err := r.client.Decr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: decr %q: %w", key, err)
	}
	return val, nil
}

// IncrBy 对key的值进行指定步长的自增操作
func (r *RedisClient) IncrBy(ctx context.Context, key string, step int64) (int64, error) {
	val, err := r.client.IncrBy(ctx, key, step).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: incrby %q: %w", key, err)
	}
	return val, nil
}

// DecrBy 对key的值进行指定步长的自减操作
func (r *RedisClient) DecrBy(ctx context.Context, key string, step int64) (int64, error) {
	val, err := r.client.DecrBy(ctx, key, step).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: decrby %q: %w", key, err)
	}
	return val, nil
}

// SAdd 向集合中添加元素
func (r *RedisClient) SAdd(ctx context.Context, key string, members ...interface{}) (int64, error) {
	count, err := r.client.SAdd(ctx, key, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: sadd %q: %w", key, err)
	}
	return count, nil
}

// SMembers 获取集合中的所有元素
func (r *RedisClient) SMembers(ctx context.Context, key string) ([]string, error) {
	members, err := r.client.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: smembers %q: %w", key, err)
	}
	return members, nil
}

// SIsMember 检查元素是否在集合中
func (r *RedisClient) SIsMember(ctx context.Context, key string, member interface{}) (bool, error) {
	exists, err := r.client.SIsMember(ctx, key, member).Result()
	if err != nil {
		return false, fmt.Errorf("cache: sismember %q: %w", key, err)
	}
	return exists, nil
}

// SCard 获取集合中元素的数量
func (r *RedisClient) SCard(ctx context.Context, key string) (int64, error) {
	count, err := r.client.SCard(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: scard %q: %w", key, err)
	}
	return count, nil
}

// SetNX 仅当 key 不存在时写入，常用于分布式锁和幂等去重
func (r *RedisClient) SetNX(ctx context.Context, key string, val any, ttl time.Duration) (bool, error) {
	result, err := r.client.SetNX(ctx, key, val, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cache: setnx %q: %w", key, err)
	}
	return result, nil
}

// SRem 从集合中删除指定成员
func (r *RedisClient) SRem(ctx context.Context, key string, members ...interface{}) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	count, err := r.client.SRem(ctx, key, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: srem %q: %w", key, err)
	}
	return count, nil
}

// SInter 获取多个集合的交集
func (r *RedisClient) SInter(ctx context.Context, keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	result, err := r.client.SInter(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: sinter %v: %w", keys, err)
	}
	return result, nil
}

// SUnion 获取多个集合的并集
func (r *RedisClient) SUnion(ctx context.Context, keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	result, err := r.client.SUnion(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: sunion %v: %w", keys, err)
	}
	return result, nil
}

// SDiff 获取多个集合的差集
func (r *RedisClient) SDiff(ctx context.Context, keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	result, err := r.client.SDiff(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: sdiff %v: %w", keys, err)
	}
	return result, nil
}

// HDel 删除哈希表中的指定字段
func (r *RedisClient) HDel(ctx context.Context, key string, fields ...string) (int64, error) {
	if len(fields) == 0 {
		return 0, nil
	}
	count, err := r.client.HDel(ctx, key, fields...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: hdel %q: %w", key, err)
	}
	return count, nil
}

// HExists 检查哈希表中的字段是否存在
func (r *RedisClient) HExists(ctx context.Context, key, field string) (bool, error) {
	exists, err := r.client.HExists(ctx, key, field).Result()
	if err != nil {
		return false, fmt.Errorf("cache: hexists %q:%q: %w", key, field, err)
	}
	return exists, nil
}

// HKeys 获取哈希表中的所有字段名
func (r *RedisClient) HKeys(ctx context.Context, key string) ([]string, error) {
	keys, err := r.client.HKeys(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: hkeys %q: %w", key, err)
	}
	return keys, nil
}

// HVals 获取哈希表中的所有字段值
func (r *RedisClient) HVals(ctx context.Context, key string) ([]string, error) {
	vals, err := r.client.HVals(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: hvals %q: %w", key, err)
	}
	return vals, nil
}

// HLen 获取哈希表中的字段数量
func (r *RedisClient) HLen(ctx context.Context, key string) (int64, error) {
	count, err := r.client.HLen(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: hlen %q: %w", key, err)
	}
	return count, nil
}

// HIncrBy 对哈希表中的字段值进行增量操作
func (r *RedisClient) HIncrBy(ctx context.Context, key, field string, incr int64) (int64, error) {
	val, err := r.client.HIncrBy(ctx, key, field, incr).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: hincrby %q:%q: %w", key, field, err)
	}
	return val, nil
}

// ZAdd 向有序集合中添加成员
func (r *RedisClient) ZAdd(ctx context.Context, key string, members ...redis.Z) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	count, err := r.client.ZAdd(ctx, key, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zadd %q: %w", key, err)
	}
	return count, nil
}

// ZRem 从有序集合中删除指定成员
func (r *RedisClient) ZRem(ctx context.Context, key string, members ...interface{}) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	count, err := r.client.ZRem(ctx, key, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zrem %q: %w", key, err)
	}
	return count, nil
}

// ZRange 按索引区间获取有序集合成员（升序）
func (r *RedisClient) ZRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	result, err := r.client.ZRange(ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: zrange %q: %w", key, err)
	}
	return result, nil
}

// ZRevRange 按索引区间获取有序集合成员（降序）
func (r *RedisClient) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	result, err := r.client.ZRevRange(ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: zrevrange %q: %w", key, err)
	}
	return result, nil
}

// ZRangeByScore 按分数区间获取有序集合成员（升序），支持分页
func (r *RedisClient) ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) ([]string, error) {
	result, err := r.client.ZRangeByScore(ctx, key, opt).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: zrangebyscore %q: %w", key, err)
	}
	return result, nil
}

// ZRevRangeByScore 按分数区间获取有序集合成员（降序），支持分页
func (r *RedisClient) ZRevRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) ([]string, error) {
	result, err := r.client.ZRevRangeByScore(ctx, key, opt).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: zrevrangebyscore %q: %w", key, err)
	}
	return result, nil
}

// ZCard 获取有序集合的成员数量
func (r *RedisClient) ZCard(ctx context.Context, key string) (int64, error) {
	count, err := r.client.ZCard(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zcard %q: %w", key, err)
	}
	return count, nil
}

// ZCount 获取有序集合中分数在 min 和 max 之间的成员数量
func (r *RedisClient) ZCount(ctx context.Context, key, min, max string) (int64, error) {
	count, err := r.client.ZCount(ctx, key, min, max).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zcount %q: %w", key, err)
	}
	return count, nil
}

// ZScore 获取有序集合中成员的分数
func (r *RedisClient) ZScore(ctx context.Context, key, member string) (float64, error) {
	score, err := r.client.ZScore(ctx, key, member).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, fmt.Errorf("cache: zscore %q:%q: %w", key, member, err)
		}
		return 0, fmt.Errorf("cache: zscore %q:%q: %w", key, member, err)
	}
	return score, nil
}

// ZRank 获取有序集合中成员的升序排名（从 0 开始）
func (r *RedisClient) ZRank(ctx context.Context, key, member string) (int64, error) {
	rank, err := r.client.ZRank(ctx, key, member).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, fmt.Errorf("cache: zrank %q:%q: %w", key, member, err)
		}
		return 0, fmt.Errorf("cache: zrank %q:%q: %w", key, member, err)
	}
	return rank, nil
}

// ZRevRank 获取有序集合中成员的降序排名（从 0 开始）
func (r *RedisClient) ZRevRank(ctx context.Context, key, member string) (int64, error) {
	rank, err := r.client.ZRevRank(ctx, key, member).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, fmt.Errorf("cache: zrevrank %q:%q: %w", key, member, err)
		}
		return 0, fmt.Errorf("cache: zrevrank %q:%q: %w", key, member, err)
	}
	return rank, nil
}

// ZIncrBy 对有序集合中成员的分数进行增量操作
func (r *RedisClient) ZIncrBy(ctx context.Context, key, member string, increment float64) (float64, error) {
	score, err := r.client.ZIncrBy(ctx, key, increment, member).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zincrby %q:%q: %w", key, member, err)
	}
	return score, nil
}

// LPush 向列表头部插入元素
func (r *RedisClient) LPush(ctx context.Context, key string, values ...interface{}) (int64, error) {
	if len(values) == 0 {
		return 0, nil
	}
	count, err := r.client.LPush(ctx, key, values...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: lpush %q: %w", key, err)
	}
	return count, nil
}

// RPush 向列表尾部插入元素
func (r *RedisClient) RPush(ctx context.Context, key string, values ...interface{}) (int64, error) {
	if len(values) == 0 {
		return 0, nil
	}
	count, err := r.client.RPush(ctx, key, values...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: rpush %q: %w", key, err)
	}
	return count, nil
}

// LPop 从列表头部弹出元素
func (r *RedisClient) LPop(ctx context.Context, key string) (string, error) {
	val, err := r.client.LPop(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: lpop %q: %w", key, err)
	}
	return val, nil
}

// RPop 从列表尾部弹出元素
func (r *RedisClient) RPop(ctx context.Context, key string) (string, error) {
	val, err := r.client.RPop(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: rpop %q: %w", key, err)
	}
	return val, nil
}

// LRange 按索引区间获取列表元素
func (r *RedisClient) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	result, err := r.client.LRange(ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: lrange %q: %w", key, err)
	}
	return result, nil
}

// LLen 获取列表长度
func (r *RedisClient) LLen(ctx context.Context, key string) (int64, error) {
	count, err := r.client.LLen(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: llen %q: %w", key, err)
	}
	return count, nil
}

// LRem 从列表中删除指定元素，count>0 从头部删，count<0 从尾部删，count=0 删除所有
func (r *RedisClient) LRem(ctx context.Context, key string, count int64, value interface{}) (int64, error) {
	removed, err := r.client.LRem(ctx, key, count, value).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: lrem %q: %w", key, err)
	}
	return removed, nil
}

// LTrim 保留列表指定区间内的元素，删除其余
func (r *RedisClient) LTrim(ctx context.Context, key string, start, stop int64) error {
	if err := r.client.LTrim(ctx, key, start, stop).Err(); err != nil {
		return fmt.Errorf("cache: ltrim %q: %w", key, err)
	}
	return nil
}

// Scan 游标迭代当前数据库中的 key
func (r *RedisClient) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	keys, nextCursor, err := r.client.Scan(ctx, cursor, match, count).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("cache: scan %v: %w", cursor, err)
	}
	return keys, nextCursor, nil
}

// Pipeline 返回 go-redis 管道实例，用于批量执行命令减少网络往返
func (r *RedisClient) Pipeline() redis.Pipeliner {
	return r.client.Pipeline()
}

// XAdd 向 Stream 追加消息，返回消息 ID
func (r *RedisClient) XAdd(ctx context.Context, values *redis.XAddArgs) (string, error) {
	id, err := r.client.XAdd(ctx, values).Result()
	if err != nil {
		return "", fmt.Errorf("cache: xadd %q: %w", values.Stream, err)
	}
	return id, nil
}

// XRead 从多个 Stream 读取消息，阻塞时长由 block 控制（0 表示非阻塞）
func (r *RedisClient) XRead(ctx context.Context, streams *redis.XReadArgs) ([]redis.XStream, error) {
	result, err := r.client.XRead(ctx, streams).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("cache: xread: %w", err)
	}
	return result, nil
}

// XReadGroup 以消费者组模式从 Stream 读取消息
func (r *RedisClient) XReadGroup(ctx context.Context, groupConsumer *redis.XReadGroupArgs) ([]redis.XStream, error) {
	result, err := r.client.XReadGroup(ctx, groupConsumer).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("cache: xreadgroup: %w", err)
	}
	return result, nil
}

// XGroupCreate 创建消费者组，$ 表示从最新消息开始消费，0 表示从头开始
func (r *RedisClient) XGroupCreate(ctx context.Context, stream, group, start string) error {
	if err := r.client.XGroupCreate(ctx, stream, group, start).Err(); err != nil {
		return fmt.Errorf("cache: xgroup create %q %q: %w", stream, group, err)
	}
	return nil
}

// XGroupDestroy 销毁消费者组
func (r *RedisClient) XGroupDestroy(ctx context.Context, stream, group string) (int64, error) {
	count, err := r.client.XGroupDestroy(ctx, stream, group).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xgroup destroy %q %q: %w", stream, group, err)
	}
	return count, nil
}

// XAck 确认消息已被消费者处理
func (r *RedisClient) XAck(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	count, err := r.client.XAck(ctx, stream, group, ids...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xack %q %q: %w", stream, group, err)
	}
	return count, nil
}

// XDel 从 Stream 中删除指定消息
func (r *RedisClient) XDel(ctx context.Context, stream string, ids ...string) (int64, error) {
	count, err := r.client.XDel(ctx, stream, ids...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xdel %q: %w", stream, err)
	}
	return count, nil
}

// XLen 获取 Stream 中的消息数量
func (r *RedisClient) XLen(ctx context.Context, stream string) (int64, error) {
	count, err := r.client.XLen(ctx, stream).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xlen %q: %w", stream, err)
	}
	return count, nil
}

// XRange 按 ID 区间升序获取 Stream 消息
func (r *RedisClient) XRange(ctx context.Context, stream, start, stop string) ([]redis.XMessage, error) {
	result, err := r.client.XRange(ctx, stream, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xrange %q: %w", stream, err)
	}
	return result, nil
}

// XRevRange 按 ID 区间降序获取 Stream 消息
func (r *RedisClient) XRevRange(ctx context.Context, stream, start, stop string) ([]redis.XMessage, error) {
	result, err := r.client.XRevRange(ctx, stream, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xrevrange %q: %w", stream, err)
	}
	return result, nil
}

// XTrimMaxLen 按消息数量上限裁剪 Stream，retain 为保留条数
func (r *RedisClient) XTrimMaxLen(ctx context.Context, stream string, maxLen int64) (int64, error) {
	count, err := r.client.XTrimMaxLen(ctx, stream, maxLen).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xtrim %q: %w", stream, err)
	}
	return count, nil
}

// XPending 获取消费者组中待确认的消息
func (r *RedisClient) XPending(ctx context.Context, stream, group string) (*redis.XPending, error) {
	result, err := r.client.XPending(ctx, stream, group).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xpending %q %q: %w", stream, group, err)
	}
	return result, nil
}

// XPendingExt 获取消费者组中待确认消息的详细信息
func (r *RedisClient) XPendingExt(ctx context.Context, args *redis.XPendingExtArgs) ([]redis.XPendingExt, error) {
	result, err := r.client.XPendingExt(ctx, args).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xpendingext %q %q: %w", args.Stream, args.Group, err)
	}
	return result, nil
}

// XClaim 将待确认消息转移给其他消费者处理
func (r *RedisClient) XClaim(ctx context.Context, args *redis.XClaimArgs) ([]redis.XMessage, error) {
	result, err := r.client.XClaim(ctx, args).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xclaim %q %q: %w", args.Stream, args.Group, err)
	}
	return result, nil
}

// XInfoStream 获取 Stream 的元信息
func (r *RedisClient) XInfoStream(ctx context.Context, stream string) (*redis.XInfoStream, error) {
	result, err := r.client.XInfoStream(ctx, stream).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xinfo stream %q: %w", stream, err)
	}
	return result, nil
}

// XInfoGroups 获取 Stream 关联的所有消费者组信息
func (r *RedisClient) XInfoGroups(ctx context.Context, stream string) ([]redis.XInfoGroup, error) {
	result, err := r.client.XInfoGroups(ctx, stream).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xinfo groups %q: %w", stream, err)
	}
	return result, nil
}
