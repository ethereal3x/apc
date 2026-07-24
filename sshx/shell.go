package sshx

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/ethereal3x/apc/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

const (
	// SSHX_DEFAULT_SHELL_TERM 默认交互 shell 终端类型
	SSHX_DEFAULT_SHELL_TERM = "xterm-256color"
	// SSHX_DEFAULT_SHELL_ROWS 默认 PTY 行数
	SSHX_DEFAULT_SHELL_ROWS = 24
	// SSHX_DEFAULT_SHELL_COLS 默认 PTY 列数
	SSHX_DEFAULT_SHELL_COLS = 80
)

// OpenShellParams OpenShell 参数
type OpenShellParams struct {
	// Ctx 可选；非 nil 时取消会关闭本次 Shell 的 session 以打断阻塞
	// 默认不关闭 Client，避免误杀连接池中的连接；需要时设 CloseClientOnCancel
	// 注意：CloseClientOnCancel=false 时无法打断 Client.NewSession（见 createShellSession）
	Ctx context.Context
	// Client 已建立的 SSH 客户端，生命周期由调用方管理
	Client *ssh.Client
	// Term PTY 终端类型；空则使用 SSHX_DEFAULT_SHELL_TERM
	Term string
	// Rows PTY 行数；<=0 则使用 SSHX_DEFAULT_SHELL_ROWS
	Rows int
	// Cols PTY 列数；<=0 则使用 SSHX_DEFAULT_SHELL_COLS
	Cols int
	// Modes 可选终端模式，透传给 RequestPty
	Modes ssh.TerminalModes
	// Command 非空时以 Start(Command) 启动；空则 session.Shell()
	Command string
	// CombineStderr 为 true 时将远端 stderr 合并到 stdout，Shell.Stderr 为 nil
	CombineStderr bool
	// CloseClientOnCancel 为 true 时 ctx 取消会连带关闭 Client；默认 false
	CloseClientOnCancel bool
}

// Shell 表示一次交互式 SSH PTY shell 会话
//
// 仅管理自身创建的 Session 与 PTY；不进入连接池
// Stdin/Stdout/Stderr 可桥接到任意 io（如 net.Conn）；WebSocket 等由调用方自行适配
type Shell struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	Stderr io.Reader

	session shellBootstrap

	// combined output 生命周期：注册与关闭并发安全，支持 close-before-register
	combinedMu     sync.Mutex
	combinedCloser func()
	combinedClosed bool

	stopCancel func()
	closeOnce  sync.Once
	closeErr   error
	waitOnce   sync.Once
	waitErr    error
}

// shellBootstrap 抽象启动与生命周期所需的 session 能力，便于单测注入
type shellBootstrap interface {
	RequestPty(term string, height, width int, modes ssh.TerminalModes) error
	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.Reader, error)
	StderrPipe() (io.Reader, error)
	Start(cmd string) error
	Shell() error
	WindowChange(height, width int) error
	Wait() error
	Close() error
}

// cryptoSSHSession 将 *ssh.Session 适配为 shellBootstrap
type cryptoSSHSession struct {
	*ssh.Session
}

// shellPTYConfig PTY 终端参数
type shellPTYConfig struct {
	Term  string
	Rows  int
	Cols  int
	Modes ssh.TerminalModes
}

// shellBindConfig 绑定 stdio 并启动 shell 的配置
type shellBindConfig struct {
	PTY           shellPTYConfig
	Command       string
	CombineStderr bool
}

// newShellBootstrap 从 Client 创建可启动的 session；单测可替换
var newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	return &cryptoSSHSession{Session: session}, nil
}

// closeSSHClient 关闭 SSH client；单测可替换以避免依赖真实连接
var closeSSHClient = func(client *ssh.Client) error {
	if client == nil {
		return nil
	}
	return client.Close()
}

// OpenShell 在已有 *ssh.Client 上打开交互式 PTY shell
//
// 会 NewSession、RequestPty，并默认启动 login shell；可用 Command 覆盖为 Start
// NewSession 成功后立即注册 ctx 取消，使 RequestPty / Start / Shell 阻塞可被 Session.Close 打断
// ctx 取消时默认只关 Session；CloseClientOnCancel 为 true 时才关 Client
// CloseClientOnCancel=false 时无法真正中断 Client.NewSession，详见 createShellSession
// 调用方应 defer Shell.Close()；Client 生命周期仍由调用方（或连接池）管理
func OpenShell(params OpenShellParams) (*Shell, error) {
	if params.Client == nil {
		return nil, fmt.Errorf("sshx: open shell: nil client")
	}
	if params.Ctx != nil {
		if err := params.Ctx.Err(); err != nil {
			return nil, fmt.Errorf("sshx: open shell: %w", err)
		}
	}
	ptyConfig := resolveShellPTYConfig(params)

	// 创建远端 session
	session, err := createShellSession(params.Ctx, params.Client, params.CloseClientOnCancel)
	if err != nil {
		return nil, err
	}
	shell := &Shell{session: session}

	// 注册取消，确保启动阶段阻塞也能关闭 Session
	shell.attachCancel(params.Ctx, params.Client, params.CloseClientOnCancel)

	// 申请 PTY、绑定 stdio 并启动 shell
	if err := bindAndStartShell(shell, shellBindConfig{
		PTY:           ptyConfig,
		Command:       params.Command,
		CombineStderr: params.CombineStderr,
	}); err != nil {
		_ = shell.Close()
		if params.Ctx != nil {
			if ctxErr := params.Ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("sshx: open shell: %w", ctxErr)
			}
		}
		return nil, err
	}

	// 处理启动完成与 ctx 取消的竞态
	if params.Ctx != nil {
		if ctxErr := params.Ctx.Err(); ctxErr != nil {
			_ = shell.Close()
			return nil, fmt.Errorf("sshx: open shell: %w", ctxErr)
		}
	}
	return shell, nil
}

// Resize 调整远端 PTY 窗口大小
func (shell *Shell) Resize(rows, cols int) error {
	if shell == nil || shell.session == nil {
		return fmt.Errorf("sshx: resize shell: nil shell")
	}
	if rows <= 0 || cols <= 0 {
		return fmt.Errorf("sshx: resize shell: invalid size rows=%d cols=%d", rows, cols)
	}
	if err := shell.session.WindowChange(rows, cols); err != nil {
		return fmt.Errorf("sshx: resize shell: %w", err)
	}
	return nil
}

// Wait 阻塞等待远端 shell 退出
func (shell *Shell) Wait() error {
	if shell == nil || shell.session == nil {
		return fmt.Errorf("sshx: wait shell: nil shell")
	}
	shell.waitOnce.Do(func() {
		if err := shell.session.Wait(); err != nil {
			shell.waitErr = fmt.Errorf("sshx: wait shell: %w", err)
		}
	})
	return shell.waitErr
}

// Close 幂等关闭本次 shell 的 session；不关闭底层 *ssh.Client
func (shell *Shell) Close() error {
	if shell == nil {
		return nil
	}
	// 先关 Session，再关 combined pipe，最后停止取消监听
	err := shell.closeSession()
	shell.closeCombinedOutput()
	if shell.stopCancel != nil {
		shell.stopCancel()
	}
	return err
}

// closeSession 幂等关闭底层 session，供 Close 与 ctx 取消路径共用
func (shell *Shell) closeSession() error {
	shell.closeOnce.Do(func() {
		if shell.session == nil {
			return
		}
		if err := shell.session.Close(); err != nil && !IsExpectedCloseErr(err) {
			shell.closeErr = fmt.Errorf("sshx: close shell: %w", err)
		}
	})
	return shell.closeErr
}

// registerCombinedOutputCloser 注册 combined output closer；若已关闭则立即执行
func (shell *Shell) registerCombinedOutputCloser(closer func()) {
	if shell == nil || closer == nil {
		return
	}
	shell.combinedMu.Lock()
	if shell.combinedClosed {
		shell.combinedMu.Unlock()
		closer()
		return
	}
	shell.combinedCloser = closer
	shell.combinedMu.Unlock()
}

// closeCombinedOutput 幂等关闭 combined output；closer 最多执行一次且不在持锁时调用
func (shell *Shell) closeCombinedOutput() {
	if shell == nil {
		return
	}
	shell.combinedMu.Lock()
	if shell.combinedClosed {
		shell.combinedMu.Unlock()
		return
	}
	shell.combinedClosed = true
	closer := shell.combinedCloser
	shell.combinedCloser = nil
	shell.combinedMu.Unlock()
	if closer != nil {
		closer()
	}
}

// attachCancel 在 ctx 取消时关闭 session，可选连带关闭 client
func (shell *Shell) attachCancel(ctx context.Context, client *ssh.Client, closeClientOnCancel bool) {
	if ctx == nil {
		return
	}
	sessionCloser := funcCloser(func() error {
		// 先关 Session 再关 combined pipe，解除 Stdout 消费方阻塞
		err := shell.closeSession()
		shell.closeCombinedOutput()
		return err
	})
	if closeClientOnCancel && client != nil {
		clientCloser := funcCloser(func() error {
			return closeSSHClient(client)
		})
		shell.stopCancel = CloseOnCancel(ctx, sessionCloser, clientCloser)
		return
	}
	shell.stopCancel = CloseOnCancel(ctx, sessionCloser)
}

// resolveShellPTYConfig 解析 PTY 终端类型与行列默认值
func resolveShellPTYConfig(params OpenShellParams) shellPTYConfig {
	config := shellPTYConfig{
		Term:  params.Term,
		Rows:  params.Rows,
		Cols:  params.Cols,
		Modes: params.Modes,
	}
	if config.Term == "" {
		config.Term = SSHX_DEFAULT_SHELL_TERM
	}
	if config.Rows <= 0 {
		config.Rows = SSHX_DEFAULT_SHELL_ROWS
	}
	if config.Cols <= 0 {
		config.Cols = SSHX_DEFAULT_SHELL_COLS
	}
	return config
}

// createShellSession 创建 shell 用 session；ctx 取消时默认只回收 session，可选关闭 client
//
// CloseClientOnCancel=false 时，crypto/ssh 的 Client.NewSession 无法被真正中断
// 此时仅保留一个与 NewSession 生命周期绑定的 goroutine：若取消后 NewSession 仍返回，负责关闭迟到的 Session
// 不会为同一次创建再额外堆积清理 goroutine
func createShellSession(ctx context.Context, client *ssh.Client, closeClientOnCancel bool) (shellBootstrap, error) {
	if ctx == nil {
		session, err := newShellBootstrap(client)
		if err != nil {
			return nil, fmt.Errorf("sshx: create session: %w", err)
		}
		return session, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("sshx: create session: %w", err)
	}

	type createResult struct {
		session shellBootstrap
		err     error
	}
	resultCh := make(chan createResult, 1)
	var resultMu sync.Mutex
	var callerReturned bool

	go func() {
		session, err := newShellBootstrap(client)
		resultMu.Lock()
		defer resultMu.Unlock()
		if callerReturned {
			if session != nil {
				if closeErr := session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
					logger.Warn("close ssh session created after cancel", zap.Error(closeErr))
				}
			}
			return
		}
		resultCh <- createResult{session: session, err: err}
	}()

	select {
	case result := <-resultCh:
		resultMu.Lock()
		callerReturned = true
		resultMu.Unlock()
		if result.err != nil {
			return nil, fmt.Errorf("sshx: create session: %w", result.err)
		}
		if err := ctx.Err(); err != nil {
			if result.session != nil {
				if closeErr := result.session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
					logger.Warn("close ssh session after create cancel race", zap.Error(closeErr))
				}
			}
			if closeClientOnCancel {
				if closeErr := closeSSHClient(client); closeErr != nil && !IsExpectedCloseErr(closeErr) {
					logger.Warn("close ssh client after create cancel race", zap.Error(closeErr))
				}
			}
			return nil, fmt.Errorf("sshx: create session: %w", err)
		}
		return result.session, nil
	case <-ctx.Done():
		if closeClientOnCancel {
			if closeErr := closeSSHClient(client); closeErr != nil && !IsExpectedCloseErr(closeErr) {
				logger.Warn("close ssh client during shell session create cancel", zap.Error(closeErr))
			}
			result := <-resultCh
			resultMu.Lock()
			callerReturned = true
			resultMu.Unlock()
			if result.session != nil {
				if closeErr := result.session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
					logger.Warn("close ssh session created after cancel", zap.Error(closeErr))
				}
			}
			return nil, fmt.Errorf("sshx: create session: %w", ctx.Err())
		}
		// 不关 Client：无法打断 NewSession，交由创建 goroutine 关闭迟到 Session
		resultMu.Lock()
		callerReturned = true
		select {
		case result := <-resultCh:
			resultMu.Unlock()
			if result.session != nil {
				if closeErr := result.session.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
					logger.Warn("close ssh session created after cancel", zap.Error(closeErr))
				}
			}
		default:
			resultMu.Unlock()
		}
		return nil, fmt.Errorf("sshx: create session: %w", ctx.Err())
	}
}

// bindAndStartShell 申请 PTY、绑定 stdio 并启动交互 shell
func bindAndStartShell(shell *Shell, config shellBindConfig) error {
	if shell == nil || shell.session == nil {
		return fmt.Errorf("sshx: bind shell: nil session")
	}
	session := shell.session

	// 申请远端 PTY
	if err := session.RequestPty(config.PTY.Term, config.PTY.Rows, config.PTY.Cols, config.PTY.Modes); err != nil {
		return fmt.Errorf("sshx: request pty: %w", err)
	}

	// 绑定标准输入
	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("sshx: stdin pipe: %w", err)
	}

	var stdout io.Reader
	var stderr io.Reader
	if config.CombineStderr {
		// 合并 stdout/stderr 到同一并发安全管道
		stdout, err = bindCombinedOutput(shell, session)
		if err != nil {
			return err
		}
	} else {
		stdout, err = session.StdoutPipe()
		if err != nil {
			return fmt.Errorf("sshx: stdout pipe: %w", err)
		}
		stderr, err = session.StderrPipe()
		if err != nil {
			return fmt.Errorf("sshx: stderr pipe: %w", err)
		}
	}

	// 启动前暴露 stdio，使启动阶段取消也能解除 Stdout 消费方阻塞
	shell.Stdin = stdin
	shell.Stdout = stdout
	shell.Stderr = stderr

	// 启动远端 shell 或指定命令
	if config.Command != "" {
		err = session.Start(config.Command)
	} else {
		err = session.Shell()
	}
	if err != nil {
		shell.closeCombinedOutput()
		return fmt.Errorf("sshx: start shell: %w", err)
	}
	return nil
}

// bindCombinedOutput 将 stdout 与 stderr 写入同一并发安全管道，并由 Shell 持有关闭接缝
func bindCombinedOutput(shell *Shell, session shellBootstrap) (io.Reader, error) {
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("sshx: stdout pipe: %w", err)
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("sshx: stderr pipe: %w", err)
	}

	pipeReader, pipeWriter := io.Pipe()
	var writeMu sync.Mutex
	var waitGroup sync.WaitGroup
	var closeOnce sync.Once
	closeWriter := func() {
		closeOnce.Do(func() {
			_ = pipeWriter.Close()
		})
	}
	// 通过生命周期注册，保证与 Close/cancel 的 happens-before
	shell.registerCombinedOutputCloser(closeWriter)

	copyOutput := func(reader io.Reader) {
		defer waitGroup.Done()
		buffer := make([]byte, 32*1024)
		for {
			readCount, readErr := reader.Read(buffer)
			if readCount > 0 {
				writeMu.Lock()
				_, writeErr := pipeWriter.Write(buffer[:readCount])
				writeMu.Unlock()
				if writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}

	waitGroup.Add(2)
	go copyOutput(stdoutPipe)
	go copyOutput(stderrPipe)
	go func() {
		waitGroup.Wait()
		closeWriter()
	}()
	return pipeReader, nil
}
