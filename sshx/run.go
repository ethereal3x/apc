package sshx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/ethereal3x/apc/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// CommandOutput 保存命令执行产生的 stdout/stderr
type CommandOutput struct {
	Stdout string
	Stderr string
}

// Combined 合并 stdout/stderr，便于日志展示
func (output CommandOutput) Combined() string {
	if output.Stderr == "" {
		return output.Stdout
	}
	if output.Stdout == "" {
		return output.Stderr
	}
	return output.Stdout + "\nSTDERR:\n" + output.Stderr
}

// RunCommandParams RunCommand 参数
type RunCommandParams struct {
	Ctx            context.Context
	Client         *ssh.Client
	Command        string
	MaxOutputBytes int64
	Settings       Settings
}

// RunCommand 在已有的 SSH 客户端上执行一次性命令
//
// 失败时仍返回已捕获的 stdout/stderr；若远程以非零退出，错误链中保留 *ssh.ExitError
// 以便 errors.As 读取退出码
//
// MaxOutputBytes 为 stdout+stderr 合计上限，两流共享线程安全预算
// ctx 取消时关闭 session/client，等待 Session.Run 退出后再读取缓冲
// 调用方传入的 client 可能因此被关闭；若需保留 client，请用连接池重新 Get
func RunCommand(params RunCommandParams) (CommandOutput, error) {
	if params.Ctx == nil {
		return CommandOutput{}, fmt.Errorf("sshx: run command: nil context")
	}
	if params.Client == nil {
		return CommandOutput{}, fmt.Errorf("sshx: run command: nil client")
	}
	if err := params.Ctx.Err(); err != nil {
		return CommandOutput{}, fmt.Errorf("sshx: run command: %w", err)
	}
	maxBytes := params.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = params.Settings.MaxCommandOutputBytesOrDefault()
	}

	session, err := newSSHSession(params.Ctx, params.Client)
	if err != nil {
		return CommandOutput{}, err
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close ssh session", zap.Error(closeErr))
		}
	}()

	capture := newSharedOutputCapture(maxBytes)
	session.Stdout = capture.stdoutWriter()
	session.Stderr = capture.stderrWriter()

	runCh := make(chan error, 1)
	go func() {
		runCh <- session.Run(params.Command)
	}()

	var runErr error
	select {
	case runErr = <-runCh:
	case <-params.Ctx.Done():
		if closeErr := session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close ssh session on cancel", zap.Error(closeErr))
		}
		if closeErr := params.Client.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close ssh client on cancel", zap.Error(closeErr))
		}
		runErr = <-runCh
		output := capture.snapshot()
		return output, fmt.Errorf("sshx: run command %q: %w", params.Command, params.Ctx.Err())
	}

	output := capture.snapshot()
	if params.Ctx.Err() != nil {
		return output, fmt.Errorf("sshx: run command %q: %w", params.Command, params.Ctx.Err())
	}
	if runErr != nil {
		if capture.truncated() {
			return output, fmt.Errorf("sshx: command %q failed: truncated=true limit=%d stdout=%q stderr=%q: %w",
				params.Command, maxBytes, output.Stdout, output.Stderr, runErr)
		}
		return output, fmt.Errorf("sshx: command %q failed: stdout=%q stderr=%q: %w",
			params.Command, output.Stdout, output.Stderr, runErr)
	}
	if capture.truncated() {
		return output, fmt.Errorf("sshx: command %q output exceeded limit %d bytes", params.Command, maxBytes)
	}
	return output, nil
}

// ExecWithStdioParams ExecWithStdio 参数
type ExecWithStdioParams struct {
	Ctx     context.Context
	Client  *ssh.Client
	Command string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// ExecWithStdio 在远程服务器执行命令，直接连接 stdin/stdout/stderr
//
// ctx 取消时关闭 session/client，并等待 Session.Run 退出后再返回
// 保证返回后不再访问调用方提供的 Reader/Writer
func ExecWithStdio(params ExecWithStdioParams) error {
	if params.Ctx == nil {
		return fmt.Errorf("sshx: exec with stdio: nil context")
	}
	if params.Client == nil {
		return fmt.Errorf("sshx: exec with stdio: nil client")
	}
	if err := params.Ctx.Err(); err != nil {
		return fmt.Errorf("sshx: exec with stdio: %w", err)
	}
	session, err := newSSHSession(params.Ctx, params.Client)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close ssh session", zap.Error(closeErr))
		}
	}()

	if params.Stdin != nil {
		session.Stdin = params.Stdin
	}
	session.Stdout = params.Stdout
	session.Stderr = params.Stderr

	runCh := make(chan error, 1)
	go func() {
		runCh <- session.Run(params.Command)
	}()

	select {
	case err := <-runCh:
		if params.Ctx.Err() != nil {
			return fmt.Errorf("sshx: exec %q: %w", params.Command, params.Ctx.Err())
		}
		if err != nil {
			return fmt.Errorf("sshx: exec %q: %w", params.Command, err)
		}
		return nil
	case <-params.Ctx.Done():
		if closeErr := session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close ssh session on cancel", zap.Error(closeErr))
		}
		if closeErr := params.Client.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close ssh client on cancel", zap.Error(closeErr))
		}
		<-runCh
		return fmt.Errorf("sshx: exec %q: %w", params.Command, params.Ctx.Err())
	}
}

// newSSHSession 创建 SSH session；创建阻塞期间响应 ctx 取消，并处理成功与取消竞态
func newSSHSession(ctx context.Context, client *ssh.Client) (*ssh.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("sshx: create session: %w", err)
	}

	type createResult struct {
		session *ssh.Session
		err     error
	}
	done := make(chan createResult, 1)
	go func() {
		session, err := client.NewSession()
		done <- createResult{session: session, err: err}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			return nil, fmt.Errorf("sshx: create session: %w", result.err)
		}
		if err := ctx.Err(); err != nil {
			if result.session != nil {
				if closeErr := result.session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
					logger.Warn("close ssh session after create cancel race", zap.Error(closeErr))
				}
			}
			return nil, fmt.Errorf("sshx: create session: %w", err)
		}
		return result.session, nil
	case <-ctx.Done():
		if closeErr := client.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close ssh client during session create cancel", zap.Error(closeErr))
		}
		result := <-done
		if result.session != nil {
			if closeErr := result.session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
				logger.Warn("close ssh session created after cancel", zap.Error(closeErr))
			}
		}
		return nil, fmt.Errorf("sshx: create session: %w", ctx.Err())
	}
}

// sharedOutputCapture 共享 stdout/stderr 合计字节预算，并发安全
type sharedOutputCapture struct {
	mu        sync.Mutex
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	remaining int64
	isTrunc   bool
}

// newSharedOutputCapture 创建共享预算捕获器
func newSharedOutputCapture(limit int64) *sharedOutputCapture {
	return &sharedOutputCapture{remaining: limit}
}

// stdoutWriter 返回写入 stdout 的 Writer
func (capture *sharedOutputCapture) stdoutWriter() io.Writer {
	return captureWriter{capture: capture, toStderr: false}
}

// stderrWriter 返回写入 stderr 的 Writer
func (capture *sharedOutputCapture) stderrWriter() io.Writer {
	return captureWriter{capture: capture, toStderr: true}
}

// snapshot 在写侧 goroutine 退出后安全读取输出快照
func (capture *sharedOutputCapture) snapshot() CommandOutput {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return CommandOutput{
		Stdout: capture.stdout.String(),
		Stderr: capture.stderr.String(),
	}
}

// truncated 返回是否因超过合计上限而截断
func (capture *sharedOutputCapture) truncated() bool {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.isTrunc
}

// captureWriter 将写入导向共享预算捕获器
type captureWriter struct {
	capture  *sharedOutputCapture
	toStderr bool
}

// Write 按共享预算写入，超出部分丢弃并标记截断
func (writer captureWriter) Write(data []byte) (int, error) {
	writer.capture.mu.Lock()
	defer writer.capture.mu.Unlock()
	if writer.capture.remaining <= 0 {
		writer.capture.isTrunc = true
		return len(data), nil
	}
	writeSize := int64(len(data))
	if writeSize > writer.capture.remaining {
		writeSize = writer.capture.remaining
		writer.capture.isTrunc = true
	}
	var (
		n   int
		err error
	)
	if writer.toStderr {
		n, err = writer.capture.stderr.Write(data[:writeSize])
	} else {
		n, err = writer.capture.stdout.Write(data[:writeSize])
	}
	writer.capture.remaining -= int64(n)
	if err != nil {
		return n, err
	}
	return len(data), nil
}
