package message

import (
	"time"

	"github.com/ethereal3x/apc/tool"
)

// Envelope 事件信封，包装消息体用于可靠投递
// （序列化为 JSON 存入 task 表再发布到 Stream，保证消息不丢）
type Envelope struct {
	Id        string `json:"id"`        // 消息编号（幂等键）
	Timestamp int64  `json:"timestamp"` // 毫秒时间戳
	Data      any    `json:"data"`      // 消息体
}

// NewEnvelope 包装消息体为事件信封，自动生成 11 位消息编号
func NewEnvelope(data any) *Envelope {
	return &Envelope{
		Id:        tool.RandomNumeric(11),
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}
}
