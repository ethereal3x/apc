package errs

import (
	"errors"
	"fmt"
	"testing"
)

type mockErrorReply struct {
	Code    int32
	Message string
}

func (reply *mockErrorReply) SetCode(code int32) {
	reply.Code = code
}

func (reply *mockErrorReply) SetMessage(message string) {
	reply.Message = message
}

type reflectReply struct {
	Code    int32
	Message string
}

// TestAsBizErrorDirect 校验直接 BizError 可被提取
func TestAsBizErrorDirect(t *testing.T) {
	bizErr := New(ErrorCode(400), "bad request")
	got, ok := AsBizError(bizErr)
	if !ok {
		t.Fatal("expected BizError")
	}
	if got.Code != 400 || got.Msg != "bad request" {
		t.Fatalf("unexpected biz error: %+v", got)
	}
}

// TestAsBizErrorWrapped 校验 wrapped BizError 可被提取
func TestAsBizErrorWrapped(t *testing.T) {
	bizErr := New(ErrorCode(500), "internal")
	wrapped := fmt.Errorf("wrap: %w", bizErr)
	got, ok := AsBizError(wrapped)
	if !ok {
		t.Fatal("expected wrapped BizError")
	}
	if got.Code != 500 {
		t.Fatalf("unexpected code: %d", got.Code)
	}
}

// TestAsBizErrorNotBiz 校验普通 error 返回 false
func TestAsBizErrorNotBiz(t *testing.T) {
	_, ok := AsBizError(errors.New("plain"))
	if ok {
		t.Fatal("expected false for plain error")
	}
}

// TestHandleSuccess 校验 fn 成功时返回填充后的 reply
func TestHandleSuccess(t *testing.T) {
	reply := &mockErrorReply{}
	result, err := Handle(reply, func(resp *mockErrorReply) error {
		resp.Message = "ok"
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Message != "ok" {
		t.Fatalf("expected ok, got %s", result.Message)
	}
}

// TestHandleBizError 校验 BizError 写入 reply 且 gRPC error 为 nil
func TestHandleBizError(t *testing.T) {
	reply := &mockErrorReply{}
	result, err := Handle(reply, func(resp *mockErrorReply) error {
		return New(ErrorCode(403), "forbidden")
	})
	if err != nil {
		t.Fatalf("expected nil grpc error, got %v", err)
	}
	if result.Code != 403 || result.Message != "forbidden" {
		t.Fatalf("unexpected reply: code=%d message=%s", result.Code, result.Message)
	}
}

// TestHandleNonBizError 校验非 BizError 原样返回
func TestHandleNonBizError(t *testing.T) {
	reply := &mockErrorReply{}
	plainErr := errors.New("db down")
	result, err := Handle(reply, func(resp *mockErrorReply) error {
		return plainErr
	})
	if !errors.Is(err, plainErr) {
		t.Fatalf("expected plain error, got %v", err)
	}
	if result.Code != 0 {
		t.Fatalf("expected untouched reply, got code=%d", result.Code)
	}
}

// TestHandleReflectReply 校验反射降级路径写入 Code/Message
func TestHandleReflectReply(t *testing.T) {
	reply := &reflectReply{}
	result, err := Handle(reply, func(resp *reflectReply) error {
		return New(ErrorCode(101), "redis fail")
	})
	if err != nil {
		t.Fatalf("expected nil grpc error, got %v", err)
	}
	if result.Code != 101 || result.Message != "redis fail" {
		t.Fatalf("unexpected reflect reply: %+v", result)
	}
}

// TestHandleValueSuccess 校验 HandleValue 成功路径
func TestHandleValueSuccess(t *testing.T) {
	reply := &mockErrorReply{}
	result, err := HandleValue(reply, func() (string, error) {
		return "alice", nil
	}, func(resp *mockErrorReply, name string) error {
		resp.Message = name
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Message != "alice" {
		t.Fatalf("expected alice, got %s", result.Message)
	}
}

// TestHandleValueBizError 校验 HandleValue 业务错误路径
func TestHandleValueBizError(t *testing.T) {
	reply := &mockErrorReply{}
	result, err := HandleValue(reply, func() (string, error) {
		return "", New(ErrorCode(404), "not found")
	}, func(resp *mockErrorReply, name string) error {
		resp.Message = name
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil grpc error, got %v", err)
	}
	if result.Code != 404 || result.Message != "not found" {
		t.Fatalf("unexpected reply: code=%d message=%s", result.Code, result.Message)
	}
}
