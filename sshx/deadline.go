package sshx

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// deadlineConn 为不支持原生 SetDeadline 的连接（如 SSH channel）提供超时中断适配
//
// 重要：deadline 到期会永久关闭底层 channel，连接不可恢复
// 这不是完整可恢复的 net.Conn deadline 语义，仅用于打断阻塞读写
type deadlineConn struct {
	net.Conn
	mu               sync.Mutex
	closed           bool
	deadlineExceeded bool
	readExceeded     bool
	writeExceeded    bool
	closeOnce        sync.Once
	closeDone        chan struct{}
	closeErr         error
	readGeneration   uint64
	writeGeneration  uint64
	readTimer        *time.Timer
	writeTimer       *time.Timer
}

// wrapDeadlineConn 包装连接以支持基于 Close 的超时中断
func wrapDeadlineConn(conn net.Conn) net.Conn {
	return &deadlineConn{
		Conn:      conn,
		closeDone: make(chan struct{}),
	}
}

// Read 代理到底层连接；若因 deadline 关闭则返回 os.ErrDeadlineExceeded
func (conn *deadlineConn) Read(buffer []byte) (int, error) {
	if err := conn.checkExceeded(true); err != nil {
		return 0, err
	}
	n, err := conn.Conn.Read(buffer)
	if err == nil {
		if checkErr := conn.checkExceeded(true); checkErr != nil {
			return n, checkErr
		}
		return n, nil
	}
	return n, conn.mapDeadlineError(err, true)
}

// Write 代理到底层连接；若因 deadline 关闭则返回 os.ErrDeadlineExceeded
func (conn *deadlineConn) Write(buffer []byte) (int, error) {
	if err := conn.checkExceeded(false); err != nil {
		return 0, err
	}
	n, err := conn.Conn.Write(buffer)
	if err == nil {
		if checkErr := conn.checkExceeded(false); checkErr != nil {
			return n, checkErr
		}
		return n, nil
	}
	return n, conn.mapDeadlineError(err, false)
}

// checkExceeded 在已过期 deadline 后让读写立即失败
func (conn *deadlineConn) checkExceeded(isRead bool) error {
	conn.mu.Lock()
	exceeded := conn.deadlineExceeded && ((isRead && conn.readExceeded) || (!isRead && conn.writeExceeded))
	conn.mu.Unlock()
	if !exceeded {
		return nil
	}
	return fmt.Errorf("%w", os.ErrDeadlineExceeded)
}

// mapDeadlineError 将 deadline 导致的关闭错误映射为 os.ErrDeadlineExceeded
func (conn *deadlineConn) mapDeadlineError(err error, isRead bool) error {
	conn.mu.Lock()
	exceeded := conn.deadlineExceeded && ((isRead && conn.readExceeded) || (!isRead && conn.writeExceeded))
	conn.mu.Unlock()
	if !exceeded {
		return err
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return err
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
		return fmt.Errorf("%w", os.ErrDeadlineExceeded)
	}
	return fmt.Errorf("%w: %v", os.ErrDeadlineExceeded, err)
}

// Close 关闭底层连接并停止全部 deadline 定时器；所有调用方等待同一次关闭完成
func (conn *deadlineConn) Close() error {
	conn.mu.Lock()
	conn.closed = true
	conn.stopReadTimerLocked()
	conn.stopWriteTimerLocked()
	conn.mu.Unlock()
	return conn.closeUnderlying()
}

// closeUnderlying 确保底层 Conn.Close 只执行一次，并让并发 Close 等待完成
func (conn *deadlineConn) closeUnderlying() error {
	conn.closeOnce.Do(func() {
		conn.closeErr = conn.Conn.Close()
		close(conn.closeDone)
	})
	<-conn.closeDone
	return conn.closeErr
}

// SetDeadline 同时设置读写超时；到期后永久关闭 channel
func (conn *deadlineConn) SetDeadline(deadline time.Time) error {
	conn.mu.Lock()
	if conn.closed || conn.deadlineExceeded {
		conn.mu.Unlock()
		return net.ErrClosed
	}
	conn.stopReadTimerLocked()
	conn.stopWriteTimerLocked()
	conn.readGeneration++
	conn.writeGeneration++
	readGeneration := conn.readGeneration
	writeGeneration := conn.writeGeneration
	if deadline.IsZero() {
		conn.mu.Unlock()
		return nil
	}
	delay := time.Until(deadline)
	if delay <= 0 {
		conn.beginDeadlineCloseLocked(true, true)
		conn.mu.Unlock()
		conn.closeUnderlyingAsync()
		return nil
	}
	conn.readTimer = time.AfterFunc(delay, func() {
		conn.fireDeadline(readGeneration, true, false)
	})
	conn.writeTimer = time.AfterFunc(delay, func() {
		conn.fireDeadline(writeGeneration, false, true)
	})
	conn.mu.Unlock()
	return nil
}

// SetReadDeadline 设置读超时；到期后永久关闭 channel
// 到期后的连接不可恢复，再次调用返回 net.ErrClosed
func (conn *deadlineConn) SetReadDeadline(deadline time.Time) error {
	conn.mu.Lock()
	if conn.closed || conn.deadlineExceeded {
		conn.mu.Unlock()
		return net.ErrClosed
	}
	conn.stopReadTimerLocked()
	conn.readGeneration++
	generation := conn.readGeneration
	if deadline.IsZero() {
		conn.mu.Unlock()
		return nil
	}
	delay := time.Until(deadline)
	if delay <= 0 {
		conn.beginDeadlineCloseLocked(true, false)
		conn.mu.Unlock()
		conn.closeUnderlyingAsync()
		return nil
	}
	conn.readTimer = time.AfterFunc(delay, func() {
		conn.fireDeadline(generation, true, false)
	})
	conn.mu.Unlock()
	return nil
}

// SetWriteDeadline 设置写超时；到期后永久关闭 channel
// 到期后的连接不可恢复，再次调用返回 net.ErrClosed
func (conn *deadlineConn) SetWriteDeadline(deadline time.Time) error {
	conn.mu.Lock()
	if conn.closed || conn.deadlineExceeded {
		conn.mu.Unlock()
		return net.ErrClosed
	}
	conn.stopWriteTimerLocked()
	conn.writeGeneration++
	generation := conn.writeGeneration
	if deadline.IsZero() {
		conn.mu.Unlock()
		return nil
	}
	delay := time.Until(deadline)
	if delay <= 0 {
		conn.beginDeadlineCloseLocked(false, true)
		conn.mu.Unlock()
		conn.closeUnderlyingAsync()
		return nil
	}
	conn.writeTimer = time.AfterFunc(delay, func() {
		conn.fireDeadline(generation, false, true)
	})
	conn.mu.Unlock()
	return nil
}

// fireDeadline 在 timer generation 仍有效时触发永久关闭
func (conn *deadlineConn) fireDeadline(generation uint64, readSide, writeSide bool) {
	conn.mu.Lock()
	if conn.closed || conn.deadlineExceeded {
		conn.mu.Unlock()
		return
	}
	if readSide && conn.readGeneration != generation {
		conn.mu.Unlock()
		return
	}
	if writeSide && conn.writeGeneration != generation {
		conn.mu.Unlock()
		return
	}
	conn.beginDeadlineCloseLocked(readSide, writeSide)
	conn.mu.Unlock()
	conn.closeUnderlyingAsync()
}

// beginDeadlineCloseLocked 标记 deadline 超限并准备关闭，调用方须持有 mu
// 不在持锁时等待底层 Close，避免与依赖读写退出的 Close 死锁
// 底层即将永久关闭，读写两侧都应立即表现为 deadline exceeded
func (conn *deadlineConn) beginDeadlineCloseLocked(readSide, writeSide bool) {
	conn.deadlineExceeded = true
	conn.readExceeded = true
	conn.writeExceeded = true
	_ = readSide
	_ = writeSide
	conn.stopReadTimerLocked()
	conn.stopWriteTimerLocked()
}

// closeUnderlyingAsync 在持锁外启动底层关闭，供 deadline 路径使用
func (conn *deadlineConn) closeUnderlyingAsync() {
	go func() {
		_ = conn.closeUnderlying()
	}()
}

// stopReadTimerLocked 停止读超时定时器，调用方须持有 mu
func (conn *deadlineConn) stopReadTimerLocked() {
	if conn.readTimer != nil {
		_ = conn.readTimer.Stop()
		conn.readTimer = nil
	}
}

// stopWriteTimerLocked 停止写超时定时器，调用方须持有 mu
func (conn *deadlineConn) stopWriteTimerLocked() {
	if conn.writeTimer != nil {
		_ = conn.writeTimer.Stop()
		conn.writeTimer = nil
	}
}
