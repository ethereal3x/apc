// Package sshx 提供 SSH 连接池、命令执行 / 交互 Shell(PTY) / SFTP、KeepAlive 调优，以及 SSH 跳板链拨号
//
// 所有 API 只接受 *ssh.Client / net.Conn / io 等通用类型，不含业务语义
// 连接建立与生命周期由调用方管理；可用 ClientDialer 把跳板链拨号注入连接池
// OpenShell 只管理自身创建的 Session/PTY，不把 Shell 放入连接池
//
// OpenShell 的 ctx 取消默认只关闭本次 Session；CloseClientOnCancel=true 时才关闭 Client
// NewSession 成功后会立即注册取消，使 RequestPty / Start / Shell 阻塞可通过 Session.Close 打断
// 当 CloseClientOnCancel=false 时，crypto/ssh 的 Client.NewSession 无法被真正中断
// 此时仅保留一个与 NewSession 绑定的 goroutine，在其最终返回后关闭迟到的 Session
package sshx
