package hertz

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/ethereal3x/apc/errs"
)

// ErrorBody hertz 服务的业务错误 JSON 响应结构
type ErrorBody struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

// WriteError 将 error 映射为 JSON 响应：BizError 返回业务码和消息，
// 其他错误返回固定 500 响应，不泄漏内部细节
func WriteError(_ context.Context, c *app.RequestContext, err error) {
	if err == nil {
		return
	}
	if bizErr, ok := errs.AsBizError(err); ok {
		c.JSON(http.StatusOK, ErrorBody{Code: int32(bizErr.Code), Message: bizErr.Msg})
		return
	}
	c.JSON(http.StatusInternalServerError, ErrorBody{Code: -1, Message: "internal server error"})
}
