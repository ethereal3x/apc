package errs

import (
	"fmt"
	"reflect"
)

// ErrorCode 业务错误码类型
type ErrorCode int32

// BizError 业务错误
type BizError struct {
	Code ErrorCode
	Msg  string
}

// Error 实现 error 接口
func (e *BizError) Error() string {
	return e.Msg
}

// ErrorReply proto 响应结构体可选实现的接口，用于高效写入错误码和消息
type ErrorReply interface {
	SetCode(int32)
	SetMessage(string)
}

// New 创建 BizError 并返回 error 接口
func New(code ErrorCode, msg string) error {
	return &BizError{Code: code, Msg: msg}
}

// newBizError 创建 BizError 指针
func newBizError(code ErrorCode, msg string) *BizError {
	return &BizError{Code: code, Msg: msg}
}

// SetErrMsg 将 BizError 的 Code/Msg 写入 rsp，优先使用接口，其次反射
func SetErrMsg(rsp interface{}, eFrom error) error {
	bizErr, ok := eFrom.(*BizError)
	if !ok {
		return eFrom
	}
	if reply, ok := rsp.(ErrorReply); ok {
		reply.SetCode(int32(bizErr.Code))
		reply.SetMessage(bizErr.Msg)
		return nil
	}
	return setErrMsgByReflect(rsp, bizErr)
}

// setErrMsgByReflect 通过反射写入 Code/Message 字段，作为兼容旧 proto 的降级方案
func setErrMsgByReflect(rsp interface{}, bizErr *BizError) error {
	value := reflect.ValueOf(rsp)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return fmt.Errorf("SetErrMsg: rsp must be non-nil pointer to struct")
	}
	elem := value.Elem()
	if elem.Kind() != reflect.Struct {
		return fmt.Errorf("SetErrMsg: rsp must be pointer to struct")
	}
	codeField := elem.FieldByName("Code")
	if codeField.IsValid() && codeField.CanSet() && codeField.Kind() == reflect.Int32 {
		codeField.SetInt(int64(bizErr.Code))
	}
	msgField := elem.FieldByName("Message")
	if msgField.IsValid() && msgField.CanSet() && msgField.Kind() == reflect.String {
		msgField.SetString(bizErr.Msg)
	}
	return nil
}

// GenProtoReply proto 数据返回通用包装，将 error 中的业务错误码写入 rsp
func GenProtoReply[T any](rsp T, err error) (T, error) {
	if err == nil {
		return rsp, nil
	}
	if setErr := SetErrMsg(rsp, err); setErr != nil {
		return rsp, setErr
	}
	return rsp, nil
}
