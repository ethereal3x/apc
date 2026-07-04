// Package message 提供 Redis Streams 事件发布与消费能力
package message

import (
	"context"
	"fmt"

	"github.com/ethereal3x/apc/logger"
	"github.com/ethereal3x/apc/tracing"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Publisher Redis Streams 事件发布器
type Publisher struct {
	client *redis.Client
}

// NewPublisher 创建事件发布器
func NewPublisher(client *redis.Client) *Publisher {
	return &Publisher{client: client}
}

// Publish 发布事件消息 JSON 到指定 stream
func (p *Publisher) Publish(ctx context.Context, topic, messageJSON string) error {
	ctx, span := tracing.Start(ctx, "message.Publish "+topic)
	defer span.End()

	_, err := p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: topic,
		Values: map[string]interface{}{"data": messageJSON},
	}).Result()
	if err != nil {
		tracing.RecordError(ctx, err)
		logger.ContextError(ctx, "publish event failed", zap.String("topic", topic), zap.Error(err))
		return fmt.Errorf("xadd %s: %w", topic, err)
	}
	logger.ContextInfo(ctx, "publish event", zap.String("topic", topic))
	return nil
}
