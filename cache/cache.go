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

// SetNX 仅当 key 不存在时写入，常用于分布式锁和幂等去重
func (r *RedisClient) SetNX(key string, val any, ttl time.Duration) (bool, error) {
	result, err := r.client.SetNX(r.ctx, key, val, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cache: setnx %q: %w", key, err)
	}
	return result, nil
}

// SRem 从集合中删除指定成员
func (r *RedisClient) SRem(key string, members ...interface{}) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	count, err := r.client.SRem(r.ctx, key, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: srem %q: %w", key, err)
	}
	return count, nil
}

// SInter 获取多个集合的交集
func (r *RedisClient) SInter(keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	result, err := r.client.SInter(r.ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: sinter %v: %w", keys, err)
	}
	return result, nil
}

// SUnion 获取多个集合的并集
func (r *RedisClient) SUnion(keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	result, err := r.client.SUnion(r.ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: sunion %v: %w", keys, err)
	}
	return result, nil
}

// SDiff 获取多个集合的差集
func (r *RedisClient) SDiff(keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	result, err := r.client.SDiff(r.ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: sdiff %v: %w", keys, err)
	}
	return result, nil
}

// HDel 删除哈希表中的指定字段
func (r *RedisClient) HDel(key string, fields ...string) (int64, error) {
	if len(fields) == 0 {
		return 0, nil
	}
	count, err := r.client.HDel(r.ctx, key, fields...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: hdel %q: %w", key, err)
	}
	return count, nil
}

// HExists 检查哈希表中的字段是否存在
func (r *RedisClient) HExists(key, field string) (bool, error) {
	exists, err := r.client.HExists(r.ctx, key, field).Result()
	if err != nil {
		return false, fmt.Errorf("cache: hexists %q:%q: %w", key, field, err)
	}
	return exists, nil
}

// HKeys 获取哈希表中的所有字段名
func (r *RedisClient) HKeys(key string) ([]string, error) {
	keys, err := r.client.HKeys(r.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: hkeys %q: %w", key, err)
	}
	return keys, nil
}

// HVals 获取哈希表中的所有字段值
func (r *RedisClient) HVals(key string) ([]string, error) {
	vals, err := r.client.HVals(r.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: hvals %q: %w", key, err)
	}
	return vals, nil
}

// HLen 获取哈希表中的字段数量
func (r *RedisClient) HLen(key string) (int64, error) {
	count, err := r.client.HLen(r.ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: hlen %q: %w", key, err)
	}
	return count, nil
}

// HIncrBy 对哈希表中的字段值进行增量操作
func (r *RedisClient) HIncrBy(key, field string, incr int64) (int64, error) {
	val, err := r.client.HIncrBy(r.ctx, key, field, incr).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: hincrby %q:%q: %w", key, field, err)
	}
	return val, nil
}

// ZAdd 向有序集合中添加成员
func (r *RedisClient) ZAdd(key string, members ...redis.Z) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	count, err := r.client.ZAdd(r.ctx, key, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zadd %q: %w", key, err)
	}
	return count, nil
}

// ZRem 从有序集合中删除指定成员
func (r *RedisClient) ZRem(key string, members ...interface{}) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	count, err := r.client.ZRem(r.ctx, key, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zrem %q: %w", key, err)
	}
	return count, nil
}

// ZRange 按索引区间获取有序集合成员（升序）
func (r *RedisClient) ZRange(key string, start, stop int64) ([]string, error) {
	result, err := r.client.ZRange(r.ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: zrange %q: %w", key, err)
	}
	return result, nil
}

// ZRevRange 按索引区间获取有序集合成员（降序）
func (r *RedisClient) ZRevRange(key string, start, stop int64) ([]string, error) {
	result, err := r.client.ZRevRange(r.ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: zrevrange %q: %w", key, err)
	}
	return result, nil
}

// ZRangeByScore 按分数区间获取有序集合成员（升序），支持分页
func (r *RedisClient) ZRangeByScore(key string, opt *redis.ZRangeBy) ([]string, error) {
	result, err := r.client.ZRangeByScore(r.ctx, key, opt).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: zrangebyscore %q: %w", key, err)
	}
	return result, nil
}

// ZRevRangeByScore 按分数区间获取有序集合成员（降序），支持分页
func (r *RedisClient) ZRevRangeByScore(key string, opt *redis.ZRangeBy) ([]string, error) {
	result, err := r.client.ZRevRangeByScore(r.ctx, key, opt).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: zrevrangebyscore %q: %w", key, err)
	}
	return result, nil
}

// ZCard 获取有序集合的成员数量
func (r *RedisClient) ZCard(key string) (int64, error) {
	count, err := r.client.ZCard(r.ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zcard %q: %w", key, err)
	}
	return count, nil
}

// ZCount 获取有序集合中分数在 min 和 max 之间的成员数量
func (r *RedisClient) ZCount(key, min, max string) (int64, error) {
	count, err := r.client.ZCount(r.ctx, key, min, max).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zcount %q: %w", key, err)
	}
	return count, nil
}

// ZScore 获取有序集合中成员的分数
func (r *RedisClient) ZScore(key, member string) (float64, error) {
	score, err := r.client.ZScore(r.ctx, key, member).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, fmt.Errorf("cache: zscore %q:%q: %w", key, member, err)
		}
		return 0, fmt.Errorf("cache: zscore %q:%q: %w", key, member, err)
	}
	return score, nil
}

// ZRank 获取有序集合中成员的升序排名（从 0 开始）
func (r *RedisClient) ZRank(key, member string) (int64, error) {
	rank, err := r.client.ZRank(r.ctx, key, member).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, fmt.Errorf("cache: zrank %q:%q: %w", key, member, err)
		}
		return 0, fmt.Errorf("cache: zrank %q:%q: %w", key, member, err)
	}
	return rank, nil
}

// ZRevRank 获取有序集合中成员的降序排名（从 0 开始）
func (r *RedisClient) ZRevRank(key, member string) (int64, error) {
	rank, err := r.client.ZRevRank(r.ctx, key, member).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, fmt.Errorf("cache: zrevrank %q:%q: %w", key, member, err)
		}
		return 0, fmt.Errorf("cache: zrevrank %q:%q: %w", key, member, err)
	}
	return rank, nil
}

// ZIncrBy 对有序集合中成员的分数进行增量操作
func (r *RedisClient) ZIncrBy(key, member string, increment float64) (float64, error) {
	score, err := r.client.ZIncrBy(r.ctx, key, increment, member).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: zincrby %q:%q: %w", key, member, err)
	}
	return score, nil
}

// LPush 向列表头部插入元素
func (r *RedisClient) LPush(key string, values ...interface{}) (int64, error) {
	if len(values) == 0 {
		return 0, nil
	}
	count, err := r.client.LPush(r.ctx, key, values...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: lpush %q: %w", key, err)
	}
	return count, nil
}

// RPush 向列表尾部插入元素
func (r *RedisClient) RPush(key string, values ...interface{}) (int64, error) {
	if len(values) == 0 {
		return 0, nil
	}
	count, err := r.client.RPush(r.ctx, key, values...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: rpush %q: %w", key, err)
	}
	return count, nil
}

// LPop 从列表头部弹出元素
func (r *RedisClient) LPop(key string) (string, error) {
	val, err := r.client.LPop(r.ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: lpop %q: %w", key, err)
	}
	return val, nil
}

// RPop 从列表尾部弹出元素
func (r *RedisClient) RPop(key string) (string, error) {
	val, err := r.client.RPop(r.ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: rpop %q: %w", key, err)
	}
	return val, nil
}

// LRange 按索引区间获取列表元素
func (r *RedisClient) LRange(key string, start, stop int64) ([]string, error) {
	result, err := r.client.LRange(r.ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: lrange %q: %w", key, err)
	}
	return result, nil
}

// LLen 获取列表长度
func (r *RedisClient) LLen(key string) (int64, error) {
	count, err := r.client.LLen(r.ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: llen %q: %w", key, err)
	}
	return count, nil
}

// LRem 从列表中删除指定元素，count>0 从头部删，count<0 从尾部删，count=0 删除所有
func (r *RedisClient) LRem(key string, count int64, value interface{}) (int64, error) {
	removed, err := r.client.LRem(r.ctx, key, count, value).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: lrem %q: %w", key, err)
	}
	return removed, nil
}

// LTrim 保留列表指定区间内的元素，删除其余
func (r *RedisClient) LTrim(key string, start, stop int64) error {
	if err := r.client.LTrim(r.ctx, key, start, stop).Err(); err != nil {
		return fmt.Errorf("cache: ltrim %q: %w", key, err)
	}
	return nil
}

// Scan 游标迭代当前数据库中的 key
func (r *RedisClient) Scan(cursor uint64, match string, count int64) ([]string, uint64, error) {
	keys, nextCursor, err := r.client.Scan(r.ctx, cursor, match, count).Result()
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
func (r *RedisClient) XAdd(values *redis.XAddArgs) (string, error) {
	id, err := r.client.XAdd(r.ctx, values).Result()
	if err != nil {
		return "", fmt.Errorf("cache: xadd %q: %w", values.Stream, err)
	}
	return id, nil
}

// XRead 从多个 Stream 读取消息，阻塞时长由 block 控制（0 表示非阻塞）
func (r *RedisClient) XRead(streams *redis.XReadArgs) ([]redis.XStream, error) {
	result, err := r.client.XRead(r.ctx, streams).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("cache: xread: %w", err)
	}
	return result, nil
}

// XReadGroup 以消费者组模式从 Stream 读取消息
func (r *RedisClient) XReadGroup(groupConsumer *redis.XReadGroupArgs) ([]redis.XStream, error) {
	result, err := r.client.XReadGroup(r.ctx, groupConsumer).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("cache: xreadgroup: %w", err)
	}
	return result, nil
}

// XGroupCreate 创建消费者组，$ 表示从最新消息开始消费，0 表示从头开始
func (r *RedisClient) XGroupCreate(stream, group, start string) error {
	if err := r.client.XGroupCreate(r.ctx, stream, group, start).Err(); err != nil {
		return fmt.Errorf("cache: xgroup create %q %q: %w", stream, group, err)
	}
	return nil
}

// XGroupDestroy 销毁消费者组
func (r *RedisClient) XGroupDestroy(stream, group string) (int64, error) {
	count, err := r.client.XGroupDestroy(r.ctx, stream, group).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xgroup destroy %q %q: %w", stream, group, err)
	}
	return count, nil
}

// XAck 确认消息已被消费者处理
func (r *RedisClient) XAck(stream, group string, ids ...string) (int64, error) {
	count, err := r.client.XAck(r.ctx, stream, group, ids...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xack %q %q: %w", stream, group, err)
	}
	return count, nil
}

// XDel 从 Stream 中删除指定消息
func (r *RedisClient) XDel(stream string, ids ...string) (int64, error) {
	count, err := r.client.XDel(r.ctx, stream, ids...).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xdel %q: %w", stream, err)
	}
	return count, nil
}

// XLen 获取 Stream 中的消息数量
func (r *RedisClient) XLen(stream string) (int64, error) {
	count, err := r.client.XLen(r.ctx, stream).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xlen %q: %w", stream, err)
	}
	return count, nil
}

// XRange 按 ID 区间升序获取 Stream 消息
func (r *RedisClient) XRange(stream, start, stop string) ([]redis.XMessage, error) {
	result, err := r.client.XRange(r.ctx, stream, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xrange %q: %w", stream, err)
	}
	return result, nil
}

// XRevRange 按 ID 区间降序获取 Stream 消息
func (r *RedisClient) XRevRange(stream, start, stop string) ([]redis.XMessage, error) {
	result, err := r.client.XRevRange(r.ctx, stream, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xrevrange %q: %w", stream, err)
	}
	return result, nil
}

// XTrimMaxLen 按消息数量上限裁剪 Stream，retain 为保留条数
func (r *RedisClient) XTrimMaxLen(stream string, maxLen int64) (int64, error) {
	count, err := r.client.XTrimMaxLen(r.ctx, stream, maxLen).Result()
	if err != nil {
		return 0, fmt.Errorf("cache: xtrim %q: %w", stream, err)
	}
	return count, nil
}

// XPending 获取消费者组中待确认的消息
func (r *RedisClient) XPending(stream, group string) (*redis.XPending, error) {
	result, err := r.client.XPending(r.ctx, stream, group).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xpending %q %q: %w", stream, group, err)
	}
	return result, nil
}

// XPendingExt 获取消费者组中待确认消息的详细信息
func (r *RedisClient) XPendingExt(args *redis.XPendingExtArgs) ([]redis.XPendingExt, error) {
	result, err := r.client.XPendingExt(r.ctx, args).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xpendingext %q %q: %w", args.Stream, args.Group, err)
	}
	return result, nil
}

// XClaim 将待确认消息转移给其他消费者处理
func (r *RedisClient) XClaim(args *redis.XClaimArgs) ([]redis.XMessage, error) {
	result, err := r.client.XClaim(r.ctx, args).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xclaim %q %q: %w", args.Stream, args.Group, err)
	}
	return result, nil
}

// XInfoStream 获取 Stream 的元信息
func (r *RedisClient) XInfoStream(stream string) (*redis.XInfoStream, error) {
	result, err := r.client.XInfoStream(r.ctx, stream).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xinfo stream %q: %w", stream, err)
	}
	return result, nil
}

// XInfoGroups 获取 Stream 关联的所有消费者组信息
func (r *RedisClient) XInfoGroups(stream string) ([]redis.XInfoGroup, error) {
	result, err := r.client.XInfoGroups(r.ctx, stream).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: xinfo groups %q: %w", stream, err)
	}
	return result, nil
}
