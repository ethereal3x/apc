package sshx

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"sync"

	"github.com/ethereal3x/apc/logger"
	"go.uber.org/zap"
)

// IsExpectedCloseErr 判断 SSH/网络连接关闭时的预期错误
//
// 取消路径会主动 Close session/client 打断阻塞，随后的 defer 关闭会返回这些错误
// 归类为预期错误后，上层可以跳过 warn 日志，避免噪音
func IsExpectedCloseErr(err error) bool {
	return err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed)
}

// logUnexpectedCloseErr 非预期关闭错误记 warn
func logUnexpectedCloseErr(err error, message string, fields ...zap.Field) {
	if err == nil || IsExpectedCloseErr(err) {
		return
	}
	logger.Warn(message, append(fields, zap.Error(err))...)
}

// closeWithWarn 关闭 closer，非预期错误记 warn；兼容 typed nil
func closeWithWarn(closer io.Closer, message string, fields ...zap.Field) {
	if isNilIOCloser(closer) {
		return
	}
	logUnexpectedCloseErr(closer.Close(), message, fields...)
}

// isNilIOCloser 判断 closer 是否为 nil 或带类型的 nil 指针/接口
func isNilIOCloser(closer io.Closer) bool {
	if closer == nil {
		return true
	}
	value := reflect.ValueOf(closer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// CloseOnCancel 在 ctx 取消时关闭 closers，用于打断不感知 ctx 的阻塞 IO
//
// 返回的 stop 幂等：调用后保证 watcher 已停止或取消回调已执行完毕
// 业务成功返回前必须先 stop，再检查 ctx.Err，避免成功后仍关闭可复用 client
//
// closers 顺序应为"先关外层会话，再关底层连接"，例如 (sftpClient, sshClient)
func CloseOnCancel(ctx context.Context, closers ...io.Closer) (stop func()) {
	if ctx == nil {
		return func() {}
	}

	finished := make(chan struct{})
	var finishOnce sync.Once
	markFinished := func() {
		finishOnce.Do(func() { close(finished) })
	}

	afterStop := context.AfterFunc(ctx, func() {
		for _, closer := range closers {
			closeWithWarn(closer, "close on cancel")
		}
		markFinished()
	})

	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			if afterStop() {
				// 回调尚未开始，不会再关闭 closers
				markFinished()
				return
			}
			<-finished
		})
	}
}

// ClosersAsOne 将多个 closer 打包成单个 io.Closer
//
// 用于只接受单 closer 的 API。空切片返回 nil
// 关闭时按顺序调用每个 closer，返回第一个非预期错误
func ClosersAsOne(closers []io.Closer) io.Closer {
	if len(closers) == 0 {
		return nil
	}
	return funcCloser(func() error {
		var firstErr error
		for _, closer := range closers {
			if closer == nil {
				continue
			}
			if err := closer.Close(); err != nil && !IsExpectedCloseErr(err) && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	})
}

// funcCloser 适配函数签名到 io.Closer 接口
type funcCloser func() error

// Close 调用底层关闭函数
func (closer funcCloser) Close() error { return closer() }
