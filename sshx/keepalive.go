package sshx

import (
	"sync"
	"time"
)

// Pinger 是 *ssh.Client 用于发送 keepalive 全局请求的最小子集
//
// 定义为接口使本包与 golang.org/x/crypto/ssh 解耦，测试可用 fake 替换
// *ssh.Client 天然实现此接口
type Pinger interface {
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
}

// StartKeepalive 启动一个 goroutine，按 interval 周期发送 OpenSSH 风格的
// "keepalive@openssh.com" 全局请求，防止长连接被 NAT/防火墙空闲回收
//
// 返回的 stop 函数调用方必须在关闭连接时调用，以停止 goroutine
// stop 是幂等的，可安全多次调用
//
// interval <= 0 时不启动 goroutine，返回的 stop 是 no-op
// 若 SendRequest 返回错误，goroutine 静默退出
// 本函数不会关闭底层连接，client 的读循环会检测 EOF 并通过已有 close 路径上抛
func StartKeepalive(pinger Pinger, interval time.Duration) (stop func()) {
	if interval <= 0 {
		return func() {}
	}

	done := make(chan struct{})
	var once sync.Once
	stopFn := func() { once.Do(func() { close(done) }) }

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if _, _, err := pinger.SendRequest("keepalive@openssh.com", true, nil); err != nil {
					return
				}
			}
		}
	}()

	return stopFn
}
