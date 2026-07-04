package message

import (
	"context"
	"errors"
	"time"

	"github.com/ethereal3x/apc/logger"
	"github.com/ethereal3x/apc/tracing"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// MessageHandler 处理一条流消息（data 字段的 JSON 字符串），返回错误决定是否 ACK
type MessageHandler func(ctx context.Context, dataJSON string) error

// ConsumerConfig 消费者配置
type ConsumerConfig struct {
	Group        string        // 消费者组名
	Topic        string        // stream 名称
	Consumer     string        // 消费者名称（多副本需唯一）
	BlockTimeout time.Duration // XReadGroup 阻塞时长，默认 5s
	ClaimMinIdle time.Duration // 启动时认领 idle 超过此值的 pending 消息，默认 1m
	ClaimCount   int64         // 单次 XAutoClaim 条数，默认 100
}

// Consume 以消费者组方式持续消费指定 stream，循环直至 ctx 取消。
// 处理成功则 ACK；失败则不 ACK（待重投递），并记录日志。
// 启动时先 XAutoClaim 认领 pending 消息（崩溃恢复，PEL 不丢）
func Consume(ctx context.Context, client *redis.Client, cfg ConsumerConfig, handler MessageHandler) {
	applyConsumerDefaults(&cfg)

	_ = client.XGroupCreateMkStream(ctx, cfg.Topic, cfg.Group, "0").Err()

	logger.ContextInfo(ctx, "stream consumer started",
		zap.String("topic", cfg.Topic), zap.String("consumer", cfg.Consumer))

	claimPending(ctx, client, cfg, handler)

	for {
		if ctx.Err() != nil {
			return
		}
		streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    cfg.Group,
			Consumer: cfg.Consumer,
			Streams:  []string{cfg.Topic, ">"},
			Count:    1,
			Block:    cfg.BlockTimeout,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			logger.ContextError(ctx, "xreadgroup failed", zap.String("topic", cfg.Topic), zap.Error(err))
			time.Sleep(time.Second)
			continue
		}
		for _, stream := range streams {
			for _, msg := range stream.Messages {
				processMessage(ctx, client, cfg, msg, handler, "Consume")
			}
		}
	}
}

// applyConsumerDefaults 填充消费者配置默认值
func applyConsumerDefaults(cfg *ConsumerConfig) {
	if cfg.BlockTimeout == 0 {
		cfg.BlockTimeout = 5 * time.Second
	}
	if cfg.ClaimMinIdle == 0 {
		cfg.ClaimMinIdle = time.Minute
	}
	if cfg.ClaimCount == 0 {
		cfg.ClaimCount = 100
	}
}

// claimPending 启动时认领并处理 idle 超过阈值的 pending 消息
func claimPending(ctx context.Context, client *redis.Client, cfg ConsumerConfig, handler MessageHandler) {
	msgs, _, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   cfg.Topic,
		Group:    cfg.Group,
		Consumer: cfg.Consumer,
		MinIdle:  cfg.ClaimMinIdle,
		Start:    "0",
		Count:    cfg.ClaimCount,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		logger.ContextError(ctx, "xautoclaim failed", zap.String("topic", cfg.Topic), zap.Error(err))
		return
	}
	for _, msg := range msgs {
		processMessage(ctx, client, cfg, msg, handler, "claimPending")
	}
}

// processMessage 处理单条消息：成功 ACK，失败不 ACK 待重投递
func processMessage(ctx context.Context, client *redis.Client, cfg ConsumerConfig, msg redis.XMessage, handler MessageHandler, stage string) {
	data, _ := msg.Values["data"].(string)
	msgCtx, span := tracing.Start(ctx, "message."+stage+" "+cfg.Topic)
	defer span.End()

	if err := handler(msgCtx, data); err != nil {
		tracing.RecordError(msgCtx, err)
		logger.ContextError(msgCtx, "consume message failed",
			zap.String("topic", cfg.Topic), zap.String("id", msg.ID), zap.Error(err))
		return
	}
	_ = client.XAck(ctx, cfg.Topic, cfg.Group, msg.ID).Err()
}
