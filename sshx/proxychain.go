package sshx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/ethereal3x/apc/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// SSHX_LAYER_SSH 表示 SSH 跳板层
const SSHX_LAYER_SSH = "ssh"

// ErrUnsupportedLayer 表示遇到了不支持的跳板层类型
var ErrUnsupportedLayer = errors.New("sshx: unsupported layer type")

// DialFunc 是建立到 addr 的底层网络连接的函数签名
type DialFunc func(ctx context.Context, addr string) (net.Conn, error)

// Chain 描述一条 SSH 跳板链
//
// 空的 Layers 表示直连。可配置多层 SSH 跳板（跳板再跳跳板）
// Settings 用于默认 Direct 拨号超时与 TCP 选项
type Chain struct {
	Layers   []Layer
	Direct   DialFunc
	Settings Settings
}

// Layer 描述跳板链中的一层 SSH 跳板
type Layer struct {
	Type      string
	Name      string
	Host      string
	Port      int
	SSHConfig *ssh.ClientConfig
}

// Dial 沿 SSH 跳板链拨号到 targetAddr
//
// 返回的 net.Conn 关闭时会级联关闭链中所有 SSH 客户端
// ctx 取消时会关闭已建立的连接并返回 ctx.Err()
func (chain Chain) Dial(ctx context.Context, targetAddr string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("sshx: chain dial: %w", err)
	}
	dial := chain.Direct
	if dial == nil {
		settings := chain.Settings
		netDialer := settings.NewDialer()
		dial = func(dialCtx context.Context, addr string) (net.Conn, error) {
			conn, err := netDialer.DialContext(dialCtx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			if err := settings.ApplyTCPOptions(conn); err != nil {
				if closeErr := conn.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
					logger.Warn("close conn after apply tcp options failure", zap.Error(closeErr))
				}
				return nil, fmt.Errorf("sshx: apply tcp options: %w", err)
			}
			return conn, nil
		}
	}

	for index, layer := range chain.Layers {
		if err := validateLayer(index, layer); err != nil {
			return nil, err
		}
		layer.Type = strings.TrimSpace(layer.Type)
		prior := dial
		current := layer
		dial = func(dialCtx context.Context, addr string) (net.Conn, error) {
			client, err := dialSSHLayer(dialCtx, prior, current)
			if err != nil {
				return nil, err
			}
			conn, err := client.DialContext(dialCtx, "tcp", addr)
			if err != nil {
				if closeErr := client.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
					logger.Warn("close ssh client after dial failure", zap.Error(closeErr))
				}
				return nil, fmt.Errorf("sshx: ssh layer %q dial %s: %w", current.Name, addr, err)
			}
			wrapped := wrapDeadlineConn(conn)
			return &connWithClosers{Conn: wrapped, closers: []io.Closer{client}}, nil
		}
	}
	conn, err := dial(ctx, targetAddr)
	if err != nil {
		return nil, fmt.Errorf("sshx: chain dial %s: %w", targetAddr, err)
	}
	return conn, nil
}

// validateLayer 校验 SSH 跳板层配置
func validateLayer(index int, layer Layer) error {
	layerType := strings.TrimSpace(layer.Type)
	if layerType == "" {
		return fmt.Errorf("sshx: layer[%d] name=%q: empty type", index, layer.Name)
	}
	if layerType != SSHX_LAYER_SSH {
		return fmt.Errorf("%w: layer[%d] name=%q type=%q", ErrUnsupportedLayer, index, layer.Name, layer.Type)
	}
	if strings.TrimSpace(layer.Host) == "" {
		return fmt.Errorf("sshx: layer[%d] name=%q: empty host", index, layer.Name)
	}
	if layer.Port <= 0 || layer.Port > 65535 {
		return fmt.Errorf("sshx: layer[%d] name=%q: invalid port %d", index, layer.Name, layer.Port)
	}
	if layer.SSHConfig == nil {
		return fmt.Errorf("sshx: layer[%d] name=%q: missing SSHConfig", index, layer.Name)
	}
	return nil
}

// dialSSHLayer 通过 prior 建立到跳板的 TCP 连接，再做 SSH 握手
func dialSSHLayer(ctx context.Context, dial DialFunc, layer Layer) (*ssh.Client, error) {
	addr := net.JoinHostPort(layer.Host, strconv.Itoa(layer.Port))
	conn, err := dial(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("sshx: dial ssh layer %q: %w", layer.Name, err)
	}
	done := make(chan struct{})
	var (
		sshConn      ssh.Conn
		channels     <-chan ssh.NewChannel
		globalReqs   <-chan *ssh.Request
		handshakeErr error
	)
	go func() {
		sshConn, channels, globalReqs, handshakeErr = ssh.NewClientConn(conn, addr, layer.SSHConfig)
		close(done)
	}()
	select {
	case <-ctx.Done():
		if closeErr := conn.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close conn after ctx cancel", zap.Error(closeErr))
		}
		<-done
		if handshakeErr == nil && sshConn != nil {
			if closeErr := sshConn.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
				logger.Warn("close ssh conn after cancel race", zap.Error(closeErr))
			}
		}
		return nil, fmt.Errorf("sshx: ssh layer %q handshake: %w", layer.Name, ctx.Err())
	case <-done:
	}
	if handshakeErr != nil {
		if closeErr := conn.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close conn after handshake failure", zap.Error(closeErr))
		}
		return nil, fmt.Errorf("sshx: ssh layer %q handshake: %w", layer.Name, handshakeErr)
	}
	return ssh.NewClient(sshConn, channels, globalReqs), nil
}

// connWithClosers 包装 net.Conn，Close 时级联关闭关联的 closers
type connWithClosers struct {
	net.Conn
	closers []io.Closer
	once    sync.Once
}

// Close 关闭底层连接并级联关闭关联的 closers
func (conn *connWithClosers) Close() error {
	err := conn.Conn.Close()
	conn.once.Do(func() {
		for _, closer := range conn.closers {
			if closeErr := closer.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) && err == nil {
				err = closeErr
			}
		}
	})
	return err
}
