// Package sshx 提供 SSH 连接池、命令执行 / SFTP、KeepAlive 调优，以及 SSH 跳板链拨号
//
// 所有 API 只接受 *ssh.Client / net.Conn 等通用类型，不含业务语义
// 连接建立与生命周期由调用方管理；可用 ClientDialer 把跳板链拨号注入连接池
package sshx
