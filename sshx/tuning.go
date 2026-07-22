package sshx

import (
	"net"
	"time"
)

const (
	// SSHX_DEFAULT_KEEPALIVE_INTERVAL 默认 SSH 层心跳间隔
	SSHX_DEFAULT_KEEPALIVE_INTERVAL = 30 * time.Second
	// SSHX_DEFAULT_DIAL_TIMEOUT 默认 TCP 拨号超时
	SSHX_DEFAULT_DIAL_TIMEOUT = 30 * time.Second
	// SSHX_DEFAULT_MAX_COMMAND_OUTPUT_BYTES RunCommand 默认最大输出字节数
	SSHX_DEFAULT_MAX_COMMAND_OUTPUT_BYTES int64 = 4 << 20
)

// Settings 是应用到出站 SSH / 代理链连接的调优参数，由调用方显式注入
type Settings struct {
	// TCPNoDelay 禁用 Nagle 算法（TCP_NODELAY），让小的交互式写立即发送
	TCPNoDelay bool
	// TCPKeepAlive 启用 OS 层 TCP keepalive 探测（SO_KEEPALIVE）
	TCPKeepAlive bool
	// KeepAliveInterval 是 SSH 层 "keepalive@openssh.com" 心跳周期
	// 值 <= 0 关闭心跳
	KeepAliveInterval time.Duration
	// DialTimeout 限制 TCP 连接阶段。值 <= 0 使用 SSHX_DEFAULT_DIAL_TIMEOUT
	DialTimeout time.Duration
	// MaxCommandOutputBytes 限制 RunCommand 捕获的 stdout/stderr 总字节数
	// 值 <= 0 使用 SSHX_DEFAULT_MAX_COMMAND_OUTPUT_BYTES
	MaxCommandOutputBytes int64
}

// DefaultSettings 返回内置默认调优
func DefaultSettings() Settings {
	return Settings{
		TCPNoDelay:            true,
		TCPKeepAlive:          true,
		KeepAliveInterval:     SSHX_DEFAULT_KEEPALIVE_INTERVAL,
		DialTimeout:           SSHX_DEFAULT_DIAL_TIMEOUT,
		MaxCommandOutputBytes: SSHX_DEFAULT_MAX_COMMAND_OUTPUT_BYTES,
	}
}

// DialTimeoutOrDefault 返回配置的拨号超时，未配置时回退到默认值
func (settings Settings) DialTimeoutOrDefault() time.Duration {
	if settings.DialTimeout <= 0 {
		return SSHX_DEFAULT_DIAL_TIMEOUT
	}
	return settings.DialTimeout
}

// MaxCommandOutputBytesOrDefault 返回命令输出上限，未配置时回退到默认值
func (settings Settings) MaxCommandOutputBytesOrDefault() int64 {
	if settings.MaxCommandOutputBytes <= 0 {
		return SSHX_DEFAULT_MAX_COMMAND_OUTPUT_BYTES
	}
	return settings.MaxCommandOutputBytes
}

// ResolveKeepAlive 返回单连接的实际 keepalive 心跳间隔
// 正的 overrideSeconds 优先，否则使用 settings.KeepAliveInterval
func ResolveKeepAlive(settings Settings, overrideSeconds int) time.Duration {
	if overrideSeconds > 0 {
		return time.Duration(overrideSeconds) * time.Second
	}
	return settings.KeepAliveInterval
}

// NewDialer 构造遵循超时和 SO_KEEPALIVE 设置的 net.Dialer
//
// TCP_NODELAY 不是 Dialer 字段，需在得到的 *net.TCPConn 上调用 ApplyTCPOptions
func (settings Settings) NewDialer() *net.Dialer {
	dialer := &net.Dialer{Timeout: settings.DialTimeoutOrDefault()}
	if settings.TCPKeepAlive {
		dialer.KeepAlive = 0
	} else {
		dialer.KeepAlive = -1
	}
	return dialer
}

// ApplyTCPOptions 按 Settings 设置 TCP_NODELAY
//
// 对非 TCP 连接（如跳板机通道连接）是 no-op
func (settings Settings) ApplyTCPOptions(conn net.Conn) error {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil
	}
	return tcpConn.SetNoDelay(settings.TCPNoDelay)
}
