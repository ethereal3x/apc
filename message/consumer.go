package message

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethereal3x/apc/logger"
	"github.com/ethereal3x/apc/tracing"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// ErrConsumerClosed 消费器已关闭
var ErrConsumerClosed = errors.New("message: consumer closed")

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
	RetryDelay   time.Duration // XReadGroup 出错后的重试间隔，默认 1s
}

// Consumer 持有 Redis Stream 消费者运行所需依赖和配置
type Consumer struct {
	client  *redis.Client
	config  ConsumerConfig
	handler MessageHandler
}

// NewConsumer 创建 Redis Stream 消费者并填充默认配置
func NewConsumer(client *redis.Client, config ConsumerConfig, handler MessageHandler) *Consumer {
	applyConsumerDefaults(&config)
	return &Consumer{
		client:  client,
		config:  config,
		handler: handler,
	}
}

// Consume 运行已构造的 Redis Stream 消费者，循环直至 ctx 取消
func Consume(ctx context.Context, consumer *Consumer) error {
	if consumer == nil {
		return errors.New("message: consumer is nil")
	}
	return consumer.Run(ctx)
}

// Run 以消费者组方式持续消费指定 stream，成功处理后 ACK，失败保留待重投递
func (consumer *Consumer) Run(ctx context.Context) error {
	if err := consumer.validate(); err != nil {
		return err
	}
	if err := consumer.createConsumerGroup(ctx); err != nil {
		return fmt.Errorf("create consumer group: %w", err)
	}

	logger.ContextInfo(ctx, "stream consumer started",
		zap.String("topic", consumer.config.Topic), zap.String("consumer", consumer.config.Consumer))

	// 启动时认领 pending 消息，恢复崩溃前未 ACK 的任务
	if err := consumer.claimPending(ctx); err != nil {
		return fmt.Errorf("claim pending: %w", err)
	}

	// 持续读取新消息并按 handler 结果决定是否 ACK
	return consumer.readLoop(ctx)
}

// validate 校验消费者运行所需依赖和回调
func (consumer *Consumer) validate() error {
	if consumer == nil {
		return errors.New("message: consumer is nil")
	}
	if consumer.client == nil {
		return errors.New("message: redis client is nil")
	}
	if consumer.handler == nil {
		return errors.New("message: handler is nil")
	}
	return nil
}

// createConsumerGroup 创建消费者组，stream 不存在时自动创建
func (consumer *Consumer) createConsumerGroup(ctx context.Context) error {
	err := consumer.client.XGroupCreateMkStream(ctx, consumer.config.Topic, consumer.config.Group, "0").Err()
	if err != nil && !isBusyGroupErr(err) {
		return err
	}
	return nil
}

// isBusyGroupErr 判断是否为消费者组已存在的 BUSYGROUP 错误
func isBusyGroupErr(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP")
}

// readLoop 持续阻塞读取并处理新消息，ctx 取消时返回 ctx.Err
func (consumer *Consumer) readLoop(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		streams, err := consumer.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumer.config.Group,
			Consumer: consumer.config.Consumer,
			Streams:  []string{consumer.config.Topic, ">"},
			Count:    1,
			Block:    consumer.config.BlockTimeout,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.ContextError(ctx, "xreadgroup failed",
				zap.String("topic", consumer.config.Topic), zap.Error(err))
			if err := waitRetry(ctx, consumer.config.RetryDelay); err != nil {
				return err
			}
			continue
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				consumer.processMessage(ctx, message, "Consume")
			}
		}
	}
}

// applyConsumerDefaults 填充消费者配置默认值
func applyConsumerDefaults(config *ConsumerConfig) {
	if config.BlockTimeout == 0 {
		config.BlockTimeout = 5 * time.Second
	}
	if config.ClaimMinIdle == 0 {
		config.ClaimMinIdle = time.Minute
	}
	if config.ClaimCount == 0 {
		config.ClaimCount = 100
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Second
	}
}

// claimPending 启动时认领并处理 idle 超过阈值的 pending 消息
func (consumer *Consumer) claimPending(ctx context.Context) error {
	messages, _, err := consumer.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   consumer.config.Topic,
		Group:    consumer.config.Group,
		Consumer: consumer.config.Consumer,
		MinIdle:  consumer.config.ClaimMinIdle,
		Start:    "0",
		Count:    consumer.config.ClaimCount,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("xautoclaim: %w", err)
	}
	for _, message := range messages {
		consumer.processMessage(ctx, message, "claimPending")
	}
	return nil
}

// processMessage 处理单条消息：成功 ACK，失败不 ACK 待重投递
func (consumer *Consumer) processMessage(ctx context.Context, message redis.XMessage, stage string) {
	data, _ := message.Values["data"].(string)
	messageCtx, span := tracing.Start(ctx, "message."+stage+" "+consumer.config.Topic)
	defer span.End()

	if err := consumer.handler(messageCtx, data); err != nil {
		tracing.RecordError(messageCtx, err)
		logger.ContextError(messageCtx, "consume message failed",
			zap.String("topic", consumer.config.Topic), zap.String("id", message.ID), zap.Error(err))
		return
	}
	if err := consumer.client.XAck(ctx, consumer.config.Topic, consumer.config.Group, message.ID).Err(); err != nil {
		logger.ContextError(messageCtx, "ack message failed",
			zap.String("topic", consumer.config.Topic), zap.String("id", message.ID), zap.Error(err))
	}
}

// waitRetry 等待重试间隔，并在 ctx 取消时立即返回
func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
