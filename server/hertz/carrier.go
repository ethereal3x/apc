package hertz

import (
	"github.com/cloudwego/hertz/pkg/protocol"
)

// requestHeaderCarrier 将 hertz 请求头适配为 OpenTelemetry propagation.TextMapCarrier，
// 用于从上游请求提取 tracing 上下文
type requestHeaderCarrier struct {
	header *protocol.RequestHeader
}

// Get 返回指定请求头的值
func (carrier *requestHeaderCarrier) Get(key string) string {
	return string(carrier.header.Peek(key))
}

// Set 写入请求头，仅用于满足 carrier 接口
func (carrier *requestHeaderCarrier) Set(key, value string) {
	carrier.header.Set(key, value)
}

// Keys 返回所有请求头名称
func (carrier *requestHeaderCarrier) Keys() []string {
	keys := make([]string, 0)
	carrier.header.VisitAll(func(key, _ []byte) {
		keys = append(keys, string(key))
	})
	return keys
}
