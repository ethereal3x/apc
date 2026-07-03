package errs

import "errors"

// AsBizError 从 error 链中提取 BizError
func AsBizError(err error) (*BizError, bool) {
	var bizErr *BizError
	if errors.As(err, &bizErr) {
		return bizErr, true
	}
	return nil, false
}

// Handle 执行 fn 填充 reply，BizError 写入 reply 后以 (reply, nil) 返回
func Handle[T any](reply T, fn func(T) error) (T, error) {
	if err := fn(reply); err != nil {
		return GenProtoReply(reply, err)
	}
	return reply, nil
}

// HandleValue 执行 fn 获取结果后通过 fill 写入 reply
func HandleValue[R any, T any](reply T, fn func() (R, error), fill func(T, R) error) (T, error) {
	return Handle(reply, func(resp T) error {
		value, err := fn()
		if err != nil {
			return err
		}
		return fill(resp, value)
	})
}
