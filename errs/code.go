package errs

// 通用业务错误码
const (
	// 系统级错误 100+
	ERR_CODE_REDIS_REQUEST  ErrorCode = 101
	ERR_CODE_JSON_MARSHAL   ErrorCode = 102
	ERR_CODE_JSON_UNMARSHAL ErrorCode = 103
)

// 预定义业务错误实例
var (
	ErrRedisRequest  = newBizError(ERR_CODE_REDIS_REQUEST, "Redis请求失败")
	ErrJsonMarshal   = newBizError(ERR_CODE_JSON_MARSHAL, "Json压缩失败")
	ErrJsonUnmarshal = newBizError(ERR_CODE_JSON_UNMARSHAL, "Json解压失败")
)
