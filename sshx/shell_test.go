package sshx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// fakeShellSession 记录启动与生命周期调用，供 Shell 单测使用
type fakeShellSession struct {
	windowRows atomic.Int32
	windowCols atomic.Int32
	closes     atomic.Int32
	waits      atomic.Int32

	windowErr error
	waitErr   error
	closeErr  error
	ptyErr    error
	stdinErr  error
	stdoutErr error
	stderrErr error
	startErr  error
	shellErr  error

	blockPty     chan struct{}
	blockStart   chan struct{}
	enteredPty   chan struct{}
	enteredStart chan struct{}
	closedCh     chan struct{}
	closeOnce    sync.Once

	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	stderrReader *io.PipeReader
	stderrWriter *io.PipeWriter

	waitCh     chan struct{}
	stdinClose atomic.Int32
}

// newFakeShellSession 创建带可选阻塞点的假 session
func newFakeShellSession() *fakeShellSession {
	session := &fakeShellSession{
		closedCh: make(chan struct{}),
		waitCh:   make(chan struct{}),
	}
	session.stdinReader, session.stdinWriter = io.Pipe()
	session.stdoutReader, session.stdoutWriter = io.Pipe()
	session.stderrReader, session.stderrWriter = io.Pipe()
	return session
}

// RequestPty 申请 PTY；可在 blockPty 上阻塞，关闭时解除
func (session *fakeShellSession) RequestPty(term string, height, width int, modes ssh.TerminalModes) error {
	if session.enteredPty != nil {
		select {
		case <-session.enteredPty:
		default:
			close(session.enteredPty)
		}
	}
	if session.blockPty != nil {
		select {
		case <-session.blockPty:
		case <-session.closedCh:
			return errors.New("session closed during request pty")
		}
	}
	return session.ptyErr
}

// StdinPipe 返回标准输入管道
func (session *fakeShellSession) StdinPipe() (io.WriteCloser, error) {
	if session.stdinErr != nil {
		return nil, session.stdinErr
	}
	return &fakeStdinCloser{session: session, writer: session.stdinWriter}, nil
}

// StdoutPipe 返回标准输出管道
func (session *fakeShellSession) StdoutPipe() (io.Reader, error) {
	if session.stdoutErr != nil {
		return nil, session.stdoutErr
	}
	return session.stdoutReader, nil
}

// StderrPipe 返回标准错误管道
func (session *fakeShellSession) StderrPipe() (io.Reader, error) {
	if session.stderrErr != nil {
		return nil, session.stderrErr
	}
	return session.stderrReader, nil
}

// Start 启动指定命令；可在 blockStart 上阻塞，关闭时解除
func (session *fakeShellSession) Start(cmd string) error {
	session.signalEnteredStart()
	if session.blockStart != nil {
		select {
		case <-session.blockStart:
		case <-session.closedCh:
			return errors.New("session closed during start")
		}
	}
	return session.startErr
}

// Shell 启动 login shell；可在 blockStart 上阻塞，关闭时解除
func (session *fakeShellSession) Shell() error {
	session.signalEnteredStart()
	if session.blockStart != nil {
		select {
		case <-session.blockStart:
		case <-session.closedCh:
			return errors.New("session closed during shell")
		}
	}
	return session.shellErr
}

// signalEnteredStart 标记已进入 Start/Shell
func (session *fakeShellSession) signalEnteredStart() {
	if session.enteredStart == nil {
		return
	}
	select {
	case <-session.enteredStart:
	default:
		close(session.enteredStart)
	}
}

// WindowChange 记录窗口尺寸
func (session *fakeShellSession) WindowChange(height, width int) error {
	session.windowRows.Store(int32(height))
	session.windowCols.Store(int32(width))
	return session.windowErr
}

// Wait 等待退出信号或预设错误
func (session *fakeShellSession) Wait() error {
	session.waits.Add(1)
	if session.waitCh != nil {
		<-session.waitCh
	}
	return session.waitErr
}

// Close 记录关闭次数并广播关闭事件
func (session *fakeShellSession) Close() error {
	session.closes.Add(1)
	session.closeOnce.Do(func() {
		close(session.closedCh)
		if session.stdoutWriter != nil {
			_ = session.stdoutWriter.Close()
		}
		if session.stderrWriter != nil {
			_ = session.stderrWriter.Close()
		}
		if session.waitCh != nil {
			select {
			case <-session.waitCh:
			default:
				close(session.waitCh)
			}
		}
	})
	return session.closeErr
}

// fakeStdinCloser 记录 stdin Close，供 EOF 退出行为测试
type fakeStdinCloser struct {
	session *fakeShellSession
	writer  *io.PipeWriter
}

// Write 写入假 stdin
func (closer *fakeStdinCloser) Write(data []byte) (int, error) {
	return closer.writer.Write(data)
}

// Close 关闭假 stdin 并标记次数
func (closer *fakeStdinCloser) Close() error {
	closer.session.stdinClose.Add(1)
	return closer.writer.Close()
}

// restoreShellTestHooks 恢复创建 session / 关闭 client 的测试接缝
func restoreShellTestHooks(newBootstrap func(*ssh.Client) (shellBootstrap, error), closeClient func(*ssh.Client) error) {
	newShellBootstrap = newBootstrap
	closeSSHClient = closeClient
}

// cancelOnStartSession 在 Shell/Start 成功返回前取消 ctx，覆盖启动成功与取消竞态
type cancelOnStartSession struct {
	*fakeShellSession
	cancel context.CancelFunc
}

// Start 启动成功前取消 ctx
func (session *cancelOnStartSession) Start(cmd string) error {
	if err := session.fakeShellSession.Start(cmd); err != nil {
		return err
	}
	session.cancel()
	return nil
}

// Shell 启动成功前取消 ctx
func (session *cancelOnStartSession) Shell() error {
	if err := session.fakeShellSession.Shell(); err != nil {
		return err
	}
	session.cancel()
	return nil
}

// TestOpenShellParams 覆盖 OpenShell 参数校验与 PTY 默认值解析
func TestOpenShellParams(t *testing.T) {
	t.Run("NilClient", func(t *testing.T) {
		_, err := OpenShell(OpenShellParams{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil client")
	})

	t.Run("CanceledCtx", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := OpenShell(OpenShellParams{
			Ctx:    ctx,
			Client: &ssh.Client{},
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("ResolvePTYDefaults", func(t *testing.T) {
		config := resolveShellPTYConfig(OpenShellParams{})
		assert.Equal(t, SSHX_DEFAULT_SHELL_TERM, config.Term)
		assert.Equal(t, SSHX_DEFAULT_SHELL_ROWS, config.Rows)
		assert.Equal(t, SSHX_DEFAULT_SHELL_COLS, config.Cols)

		config = resolveShellPTYConfig(OpenShellParams{Term: "vt100", Rows: 40, Cols: 120})
		assert.Equal(t, "vt100", config.Term)
		assert.Equal(t, 40, config.Rows)
		assert.Equal(t, 120, config.Cols)
	})
}

// TestShellLifecycle 覆盖 Resize、Close 幂等、Wait 与 ctx 取消关 session
func TestShellLifecycle(t *testing.T) {
	t.Run("Resize", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		require.NoError(t, shell.Resize(40, 120))
		assert.Equal(t, int32(40), fake.windowRows.Load())
		assert.Equal(t, int32(120), fake.windowCols.Load())

		err := shell.Resize(0, 80)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid size")

		fake.windowErr = errors.New("window boom")
		err = shell.Resize(24, 80)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "window boom")
	})

	t.Run("CloseIdempotent", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		require.NoError(t, shell.Close())
		require.NoError(t, shell.Close())
		assert.Equal(t, int32(1), fake.closes.Load())
		assert.Nil(t, (*Shell)(nil).Close())
	})

	t.Run("WaitOnce", func(t *testing.T) {
		fake := newFakeShellSession()
		fake.waitErr = errors.New("exit 1")
		close(fake.waitCh)
		shell := &Shell{session: fake}
		err := shell.Wait()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exit 1")
		err = shell.Wait()
		require.Error(t, err)
		assert.Equal(t, int32(1), fake.waits.Load())
	})

	t.Run("CtxCancelClosesSession", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		ctx, cancel := context.WithCancel(context.Background())
		shell.attachCancel(ctx, nil, false)
		cancel()
		require.Eventually(t, func() bool {
			return fake.closes.Load() == 1
		}, time.Second, 5*time.Millisecond)
		require.NoError(t, shell.Close())
		assert.Equal(t, int32(1), fake.closes.Load())
	})

	t.Run("CtxCancelClosesClientWhenEnabled", func(t *testing.T) {
		fake := newFakeShellSession()
		clientCloser := &recordingCloser{}
		shell := &Shell{session: fake}
		ctx, cancel := context.WithCancel(context.Background())
		sessionCloser := funcCloser(func() error { return shell.closeSession() })
		shell.stopCancel = CloseOnCancel(ctx, sessionCloser, clientCloser)
		cancel()
		require.Eventually(t, func() bool {
			return fake.closes.Load() == 1 && clientCloser.closes.Load() == 1
		}, time.Second, 5*time.Millisecond)
		require.NoError(t, shell.Close())
	})
}

// TestCreateShellSessionCancel 覆盖 NewSession 取消路径与 CloseClientOnCancel 行为
func TestCreateShellSessionCancel(t *testing.T) {
	origBootstrap := newShellBootstrap
	origCloseClient := closeSSHClient
	t.Cleanup(func() { restoreShellTestHooks(origBootstrap, origCloseClient) })

	t.Run("CancelWithoutCloseClientClosesLateSession", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		late := newFakeShellSession()
		var clientCloseCount atomic.Int32

		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			close(started)
			<-release
			return late, nil
		}
		closeSSHClient = func(client *ssh.Client) error {
			clientCloseCount.Add(1)
			return nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			_, err := createShellSession(ctx, &ssh.Client{}, false)
			errCh <- err
		}()

		<-started
		cancel()
		err := <-errCh
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(0), clientCloseCount.Load())

		close(release)
		require.Eventually(t, func() bool {
			return late.closes.Load() == 1
		}, time.Second, 5*time.Millisecond)
	})

	t.Run("CancelWithCloseClient", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		late := newFakeShellSession()
		var clientCloseCount atomic.Int32

		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			close(started)
			<-release
			return late, nil
		}
		closeSSHClient = func(client *ssh.Client) error {
			clientCloseCount.Add(1)
			close(release)
			return nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			_, err := createShellSession(ctx, &ssh.Client{}, true)
			errCh <- err
		}()

		<-started
		cancel()
		err := <-errCh
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), clientCloseCount.Load())
		assert.Equal(t, int32(1), late.closes.Load())
	})

	t.Run("SuccessThenCancelRace", func(t *testing.T) {
		session := newFakeShellSession()
		ctx, cancel := context.WithCancel(context.Background())
		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			// NewSession 成功返回前取消，覆盖完成与取消竞态
			cancel()
			return session, nil
		}
		closeSSHClient = func(client *ssh.Client) error { return nil }

		_, err := createShellSession(ctx, &ssh.Client{}, false)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), session.closes.Load())
	})
}

// TestOpenShellStartCancel 覆盖 RequestPty/Start 阻塞期间取消与启动成败清理
func TestOpenShellStartCancel(t *testing.T) {
	origBootstrap := newShellBootstrap
	origCloseClient := closeSSHClient
	t.Cleanup(func() { restoreShellTestHooks(origBootstrap, origCloseClient) })
	closeSSHClient = func(client *ssh.Client) error { return nil }

	t.Run("CancelDuringRequestPty", func(t *testing.T) {
		fake := newFakeShellSession()
		fake.blockPty = make(chan struct{})
		fake.enteredPty = make(chan struct{})
		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			return fake, nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			_, err := OpenShell(OpenShellParams{Ctx: ctx, Client: &ssh.Client{}})
			errCh <- err
		}()

		select {
		case <-fake.enteredPty:
		case <-time.After(time.Second):
			t.Fatal("RequestPty not entered")
		}
		cancel()
		err := <-errCh
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), fake.closes.Load())
	})

	t.Run("CancelDuringShellStart", func(t *testing.T) {
		fake := newFakeShellSession()
		fake.blockStart = make(chan struct{})
		fake.enteredStart = make(chan struct{})
		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			return fake, nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			_, err := OpenShell(OpenShellParams{Ctx: ctx, Client: &ssh.Client{}})
			errCh <- err
		}()

		select {
		case <-fake.enteredStart:
		case <-time.After(time.Second):
			t.Fatal("Shell start not entered")
		}
		cancel()
		err := <-errCh
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), fake.closes.Load())
	})

	t.Run("StartFailureCleansWatcherAndSession", func(t *testing.T) {
		fake := newFakeShellSession()
		fake.shellErr = errors.New("shell boom")
		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			return fake, nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := OpenShell(OpenShellParams{Ctx: ctx, Client: &ssh.Client{}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "shell boom")
		assert.Equal(t, int32(1), fake.closes.Load())

		// watcher 已停止：再取消不应二次关闭
		cancel()
		time.Sleep(20 * time.Millisecond)
		assert.Equal(t, int32(1), fake.closes.Load())
	})

	t.Run("StartSuccess", func(t *testing.T) {
		fake := newFakeShellSession()
		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			return fake, nil
		}

		shell, err := OpenShell(OpenShellParams{Client: &ssh.Client{}, Rows: 40, Cols: 120})
		require.NoError(t, err)
		require.NotNil(t, shell)
		require.NotNil(t, shell.Stdin)
		require.NotNil(t, shell.Stdout)
		require.NotNil(t, shell.Stderr)
		require.NoError(t, shell.Close())
		assert.Equal(t, int32(1), fake.closes.Load())
	})

	t.Run("StartSuccessThenCtxRace", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		fake := &cancelOnStartSession{fakeShellSession: newFakeShellSession(), cancel: cancel}
		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			return fake, nil
		}

		_, err := OpenShell(OpenShellParams{Ctx: ctx, Client: &ssh.Client{}})
		require.ErrorIs(t, err, context.Canceled)
		assert.GreaterOrEqual(t, fake.closes.Load(), int32(1))
	})

	t.Run("CloseClientOnCancelFalseDoesNotCloseClient", func(t *testing.T) {
		fake := newFakeShellSession()
		fake.blockStart = make(chan struct{})
		fake.enteredStart = make(chan struct{})
		var clientCloseCount atomic.Int32
		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			return fake, nil
		}
		closeSSHClient = func(client *ssh.Client) error {
			clientCloseCount.Add(1)
			return nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			_, err := OpenShell(OpenShellParams{
				Ctx:                 ctx,
				Client:              &ssh.Client{},
				CloseClientOnCancel: false,
			})
			errCh <- err
		}()
		select {
		case <-fake.enteredStart:
		case <-time.After(time.Second):
			t.Fatal("Shell start not entered")
		}
		cancel()
		err := <-errCh
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(0), clientCloseCount.Load())
		assert.Equal(t, int32(1), fake.closes.Load())
	})

	t.Run("CloseClientOnCancelTrueClosesClient", func(t *testing.T) {
		fake := newFakeShellSession()
		fake.blockStart = make(chan struct{})
		fake.enteredStart = make(chan struct{})
		var clientCloseCount atomic.Int32
		newShellBootstrap = func(client *ssh.Client) (shellBootstrap, error) {
			return fake, nil
		}
		closeSSHClient = func(client *ssh.Client) error {
			clientCloseCount.Add(1)
			return nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			_, err := OpenShell(OpenShellParams{
				Ctx:                 ctx,
				Client:              &ssh.Client{},
				CloseClientOnCancel: true,
			})
			errCh <- err
		}()
		select {
		case <-fake.enteredStart:
		case <-time.After(time.Second):
			t.Fatal("Shell start not entered")
		}
		cancel()
		err := <-errCh
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), clientCloseCount.Load())
		assert.Equal(t, int32(1), fake.closes.Load())
	})
}

// TestCombineStderrStreams 覆盖合并与独立 stderr 行为
func TestCombineStderrStreams(t *testing.T) {
	t.Run("CombineTrueMergesAndNilStderr", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		require.NoError(t, bindAndStartShell(shell, shellBindConfig{
			PTY:           shellPTYConfig{Term: "xterm", Rows: 24, Cols: 80},
			CombineStderr: true,
		}))
		assert.Nil(t, shell.Stderr)
		require.NotNil(t, shell.Stdout)

		go func() {
			_, _ = io.WriteString(fake.stdoutWriter, "out")
			_ = fake.stdoutWriter.Close()
		}()
		go func() {
			_, _ = io.WriteString(fake.stderrWriter, "err")
			_ = fake.stderrWriter.Close()
		}()

		output, err := io.ReadAll(shell.Stdout)
		require.NoError(t, err)
		assert.Contains(t, string(output), "out")
		assert.Contains(t, string(output), "err")
		require.NoError(t, shell.Close())
	})

	t.Run("CombineFalseKeepsIndependentStreams", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		require.NoError(t, bindAndStartShell(shell, shellBindConfig{
			PTY:           shellPTYConfig{Term: "xterm", Rows: 24, Cols: 80},
			CombineStderr: false,
		}))
		require.NotNil(t, shell.Stdout)
		require.NotNil(t, shell.Stderr)

		go func() {
			_, _ = io.WriteString(fake.stdoutWriter, "out-only")
			_ = fake.stdoutWriter.Close()
		}()
		go func() {
			_, _ = io.WriteString(fake.stderrWriter, "err-only")
			_ = fake.stderrWriter.Close()
		}()

		var waitGroup sync.WaitGroup
		var stdoutData, stderrData []byte
		var stdoutErr, stderrErr error
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			stdoutData, stdoutErr = io.ReadAll(shell.Stdout)
		}()
		go func() {
			defer waitGroup.Done()
			stderrData, stderrErr = io.ReadAll(shell.Stderr)
		}()
		waitGroup.Wait()
		require.NoError(t, stdoutErr)
		require.NoError(t, stderrErr)
		assert.Equal(t, "out-only", string(stdoutData))
		assert.Equal(t, "err-only", string(stderrData))
		require.NoError(t, shell.Close())
	})

	t.Run("CombinedPipeUnblocksAfterClose", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		require.NoError(t, bindAndStartShell(shell, shellBindConfig{
			PTY:           shellPTYConfig{Term: "xterm", Rows: 24, Cols: 80},
			CombineStderr: true,
		}))

		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = io.Copy(io.Discard, shell.Stdout)
		}()
		require.NoError(t, shell.Close())
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("stdout reader blocked after Close")
		}
	})
}

// TestShellConcurrentCloseWaitCancel 覆盖 Close/Wait/cancel 并发与 stdin EOF 退出
func TestShellConcurrentCloseWaitCancel(t *testing.T) {
	t.Run("ConcurrentCloseWaitCancel", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		ctx, cancel := context.WithCancel(context.Background())
		shell.attachCancel(ctx, nil, false)

		var waitGroup sync.WaitGroup
		waitGroup.Add(3)
		go func() {
			defer waitGroup.Done()
			_ = shell.Wait()
		}()
		go func() {
			defer waitGroup.Done()
			_ = shell.Close()
		}()
		go func() {
			defer waitGroup.Done()
			cancel()
		}()
		waitGroup.Wait()
		assert.Equal(t, int32(1), fake.closes.Load())
		assert.Equal(t, int32(1), fake.waits.Load())
	})

	t.Run("StdinEOFAllowsWaitReturn", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		require.NoError(t, bindAndStartShell(shell, shellBindConfig{
			PTY: shellPTYConfig{Term: "xterm", Rows: 24, Cols: 80},
		}))

		connReader, connWriter := io.Pipe()
		errCh := make(chan error, 2)
		go func() {
			_, copyErr := io.Copy(shell.Stdin, connReader)
			closeErr := shell.Stdin.Close()
			if copyErr == nil {
				copyErr = closeErr
			}
			errCh <- copyErr
		}()
		go func() {
			_, copyErr := io.Copy(io.Discard, shell.Stdout)
			errCh <- copyErr
		}()

		require.NoError(t, connWriter.Close())
		require.Eventually(t, func() bool {
			return fake.stdinClose.Load() == 1
		}, time.Second, 5*time.Millisecond)

		// 模拟远端感知 stdin EOF 后退出
		_ = fake.stdoutWriter.Close()
		close(fake.waitCh)
		require.NoError(t, shell.Wait())
		require.NoError(t, shell.Close())
		<-errCh
		<-errCh
	})

	t.Run("BridgeCancelUnblocksWait", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		require.NoError(t, bindAndStartShell(shell, shellBindConfig{
			PTY: shellPTYConfig{Term: "xterm", Rows: 24, Cols: 80},
		}))
		ctx, cancel := context.WithCancel(context.Background())
		shell.attachCancel(ctx, nil, false)

		var buffer bytes.Buffer
		go func() {
			_, _ = io.Copy(&buffer, shell.Stdout)
		}()
		waitDone := make(chan error, 1)
		go func() {
			waitDone <- shell.Wait()
		}()

		cancel()
		select {
		case <-waitDone:
		case <-time.After(time.Second):
			t.Fatal("Wait blocked after ctx cancel")
		}
		require.NoError(t, shell.Close())
	})
}

// TestCombinedOutputLifecycle 覆盖 combined closer 注册与 Close/cancel 的并发及 close-before-register
func TestCombinedOutputLifecycle(t *testing.T) {
	t.Run("RegisterConcurrentWithClose", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		var executed atomic.Int32
		start := make(chan struct{})
		var waitGroup sync.WaitGroup
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			<-start
			shell.registerCombinedOutputCloser(func() { executed.Add(1) })
		}()
		go func() {
			defer waitGroup.Done()
			<-start
			_ = shell.Close()
		}()
		close(start)
		waitGroup.Wait()
		assert.Equal(t, int32(1), executed.Load())
		assert.Equal(t, int32(1), fake.closes.Load())
	})

	t.Run("RegisterConcurrentWithCancel", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		ctx, cancel := context.WithCancel(context.Background())
		shell.attachCancel(ctx, nil, false)

		var executed atomic.Int32
		start := make(chan struct{})
		var waitGroup sync.WaitGroup
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			<-start
			shell.registerCombinedOutputCloser(func() { executed.Add(1) })
		}()
		go func() {
			defer waitGroup.Done()
			<-start
			cancel()
		}()
		close(start)
		waitGroup.Wait()
		require.Eventually(t, func() bool {
			return executed.Load() == 1 && fake.closes.Load() == 1
		}, time.Second, 5*time.Millisecond)
	})

	t.Run("CancelBeforeRegisterRunsCloser", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		ctx, cancel := context.WithCancel(context.Background())
		shell.attachCancel(ctx, nil, false)

		// 先取消并确认 session 已关，再注册 closer
		sessionClosed := make(chan struct{})
		go func() {
			<-fake.closedCh
			close(sessionClosed)
		}()
		cancel()
		select {
		case <-sessionClosed:
		case <-time.After(time.Second):
			t.Fatal("session not closed after cancel")
		}

		closerDone := make(chan struct{})
		shell.registerCombinedOutputCloser(func() { close(closerDone) })
		select {
		case <-closerDone:
		case <-time.After(time.Second):
			t.Fatal("closer not run after cancel-before-register")
		}
	})

	t.Run("CloseBeforeRegisterRunsCloser", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		require.NoError(t, shell.Close())

		closerDone := make(chan struct{})
		shell.registerCombinedOutputCloser(func() { close(closerDone) })
		select {
		case <-closerDone:
		case <-time.After(time.Second):
			t.Fatal("closer not run after close-before-register")
		}
		assert.Equal(t, int32(1), fake.closes.Load())
	})

	t.Run("CloseCancelRegisterCloserOnce", func(t *testing.T) {
		fake := newFakeShellSession()
		shell := &Shell{session: fake}
		ctx, cancel := context.WithCancel(context.Background())
		shell.attachCancel(ctx, nil, false)

		var executed atomic.Int32
		start := make(chan struct{})
		var waitGroup sync.WaitGroup
		waitGroup.Add(3)
		go func() {
			defer waitGroup.Done()
			<-start
			_ = shell.Close()
		}()
		go func() {
			defer waitGroup.Done()
			<-start
			cancel()
		}()
		go func() {
			defer waitGroup.Done()
			<-start
			shell.registerCombinedOutputCloser(func() { executed.Add(1) })
		}()
		close(start)
		waitGroup.Wait()
		require.Eventually(t, func() bool {
			return executed.Load() == 1
		}, time.Second, 5*time.Millisecond)
		assert.Equal(t, int32(1), executed.Load())
		assert.Equal(t, int32(1), fake.closes.Load())
	})

	t.Run("CombineStderrCancelDuringStartUnblocksStdout", func(t *testing.T) {
		fake := newFakeShellSession()
		fake.blockStart = make(chan struct{})
		fake.enteredStart = make(chan struct{})
		shell := &Shell{session: fake}
		ctx, cancel := context.WithCancel(context.Background())
		shell.attachCancel(ctx, nil, false)

		errCh := make(chan error, 1)
		go func() {
			errCh <- bindAndStartShell(shell, shellBindConfig{
				PTY:           shellPTYConfig{Term: "xterm", Rows: 24, Cols: 80},
				CombineStderr: true,
			})
		}()
		select {
		case <-fake.enteredStart:
		case <-time.After(time.Second):
			t.Fatal("Shell start not entered")
		}

		// 启动阻塞期间 Stdout 已暴露；取消后 reader 必须退出
		require.NotNil(t, shell.Stdout)
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			_, _ = io.Copy(io.Discard, shell.Stdout)
		}()
		cancel()
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Fatal("stdout reader blocked after cancel during start")
		}
		select {
		case err := <-errCh:
			require.Error(t, err)
		case <-time.After(time.Second):
			t.Fatal("bindAndStartShell blocked after cancel")
		}
		assert.Equal(t, int32(1), fake.closes.Load())
	})
}
