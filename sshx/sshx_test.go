package sshx

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereal3x/apc/logger"
	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// TestMain 初始化测试用 logger，避免包内日志调用 panic
func TestMain(main *testing.M) {
	logger.SetLogger(logger.NewLogger(&logger.Config{
		Level:  logger.LevelError,
		Format: logger.FormatConsole,
	}))
	code := main.Run()
	_ = logger.Sync()
	os.Exit(code)
}

type recordingCloser struct{ closes atomic.Int32 }

// Close 记录关闭次数
func (closer *recordingCloser) Close() error { closer.closes.Add(1); return nil }

type errCloser struct{ err error }

// Close 返回预设错误
func (closer *errCloser) Close() error { return closer.err }

type fakePinger struct {
	pings atomic.Int32
	err   error
}

// SendRequest 记录 keepalive 调用
func (pinger *fakePinger) SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error) {
	pinger.pings.Add(1)
	if pinger.err != nil {
		return false, nil, pinger.err
	}
	return true, nil, nil
}

// gatedCloseConn 可控制底层 Close 时序的假连接
type gatedCloseConn struct {
	closeEntered chan struct{}
	releaseClose chan struct{}
	closeDone    chan struct{}
	closeCount   atomic.Int32
	closeOnce    sync.Once
}

// Close 只执行一次底层关闭，并发调用等待完成
func (conn *gatedCloseConn) Close() error {
	conn.closeOnce.Do(func() {
		conn.closeCount.Add(1)
		close(conn.closeEntered)
		<-conn.releaseClose
		close(conn.closeDone)
	})
	<-conn.closeDone
	return nil
}

// LocalAddr 返回占位地址
func (conn *gatedCloseConn) LocalAddr() net.Addr { return &net.TCPAddr{} }

// RemoteAddr 返回占位地址
func (conn *gatedCloseConn) RemoteAddr() net.Addr { return &net.TCPAddr{} }

// SetDeadline 忽略原生 deadline
func (conn *gatedCloseConn) SetDeadline(time.Time) error { return nil }

// SetReadDeadline 忽略原生读 deadline
func (conn *gatedCloseConn) SetReadDeadline(time.Time) error { return nil }

// SetWriteDeadline 忽略原生写 deadline
func (conn *gatedCloseConn) SetWriteDeadline(time.Time) error { return nil }

// Read 永久阻塞
func (conn *gatedCloseConn) Read([]byte) (int, error) { select {} }

// Write 永久阻塞
func (conn *gatedCloseConn) Write([]byte) (int, error) { select {} }

// TestKitBasics 覆盖关闭辅助、keepalive、Settings 与输出捕获
func TestKitBasics(t *testing.T) {
	assert.True(t, IsExpectedCloseErr(nil))
	assert.True(t, IsExpectedCloseErr(io.EOF))
	assert.True(t, IsExpectedCloseErr(net.ErrClosed))
	assert.False(t, IsExpectedCloseErr(errors.New("boom")))

	t.Run("CloseOnCancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		closer := &recordingCloser{}
		stop := CloseOnCancel(ctx, closer)
		cancel()
		require.Eventually(t, func() bool { return closer.closes.Load() == 1 }, time.Second, 5*time.Millisecond)
		stop()
		stop()

		closer2 := &recordingCloser{}
		stop2 := CloseOnCancel(context.Background(), closer2)
		stop2()
		time.Sleep(20 * time.Millisecond)
		assert.Equal(t, int32(0), closer2.closes.Load())
		assert.NotNil(t, CloseOnCancel(nil, closer2))
	})

	t.Run("ClosersAsOne", func(t *testing.T) {
		assert.Nil(t, ClosersAsOne(nil))
		assert.Nil(t, ClosersAsOne([]io.Closer{}))

		ok1, ok2 := &recordingCloser{}, &recordingCloser{}
		require.NoError(t, ClosersAsOne([]io.Closer{nil, ok1, nil, ok2}).Close())
		assert.Equal(t, int32(1), ok1.closes.Load())
		assert.Equal(t, int32(1), ok2.closes.Load())

		err := ClosersAsOne([]io.Closer{&errCloser{err: io.EOF}, &errCloser{err: errors.New("real")}}).Close()
		require.Error(t, err)
		assert.Equal(t, "real", err.Error())
	})

	t.Run("Keepalive", func(t *testing.T) {
		noop := &fakePinger{}
		stop := StartKeepalive(noop, 0)
		stop()
		time.Sleep(20 * time.Millisecond)
		assert.Equal(t, int32(0), noop.pings.Load())

		pinger := &fakePinger{}
		stop = StartKeepalive(pinger, 10*time.Millisecond)
		time.Sleep(55 * time.Millisecond)
		stop()
		stop()
		n := pinger.pings.Load()
		assert.GreaterOrEqual(t, n, int32(3))
		assert.LessOrEqual(t, n, int32(8))

		failing := &fakePinger{err: errors.New("closed")}
		stop = StartKeepalive(failing, 10*time.Millisecond)
		time.Sleep(30 * time.Millisecond)
		stop()
		assert.Equal(t, int32(1), failing.pings.Load())
	})

	t.Run("Settings", func(t *testing.T) {
		settings := DefaultSettings()
		assert.True(t, settings.TCPNoDelay)
		assert.Equal(t, SSHX_DEFAULT_KEEPALIVE_INTERVAL, settings.KeepAliveInterval)
		assert.Equal(t, SSHX_DEFAULT_DIAL_TIMEOUT, Settings{}.DialTimeoutOrDefault())
		assert.Equal(t, 5*time.Second, ResolveKeepAlive(settings, 5))
		assert.Equal(t, time.Duration(-1), Settings{TCPKeepAlive: false}.NewDialer().KeepAlive)
	})

	t.Run("Output", func(t *testing.T) {
		assert.Equal(t, "out\nSTDERR:\nerr", CommandOutput{Stdout: "out", Stderr: "err"}.Combined())
		capture := newSharedOutputCapture(6)
		_, err := capture.stdoutWriter().Write([]byte("abcd"))
		require.NoError(t, err)
		_, err = capture.stderrWriter().Write([]byte("efgh"))
		require.NoError(t, err)
		assert.True(t, capture.truncated())
		snap := capture.snapshot()
		assert.Equal(t, "abcd", snap.Stdout)
		assert.Equal(t, "ef", snap.Stderr)
	})
}

// TestDeadlineConn 覆盖 deadline 超时、清除、并发关闭与过期读写
func TestDeadlineConn(t *testing.T) {
	t.Run("ExpiresBlockedRead", func(t *testing.T) {
		left, right := net.Pipe()
		defer right.Close()
		wrapped := wrapDeadlineConn(left)
		require.NoError(t, wrapped.SetReadDeadline(time.Now().Add(30*time.Millisecond)))
		errCh := make(chan error, 1)
		go func() {
			_, err := wrapped.Read(make([]byte, 8))
			errCh <- err
		}()
		select {
		case err := <-errCh:
			assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
		case <-time.After(time.Second):
			t.Fatal("blocked read did not unblock")
		}
	})

	t.Run("ClearAndExtend", func(t *testing.T) {
		left, right := net.Pipe()
		defer left.Close()
		defer right.Close()
		wrapped := wrapDeadlineConn(left)
		require.NoError(t, wrapped.SetReadDeadline(time.Now().Add(30*time.Millisecond)))
		require.NoError(t, wrapped.SetReadDeadline(time.Time{}))
		time.Sleep(60 * time.Millisecond)
		go func() { _, _ = right.Write([]byte("ok")) }()
		buf := make([]byte, 2)
		n, err := wrapped.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "ok", string(buf[:n]))
	})

	t.Run("ConcurrentCloseWaits", func(t *testing.T) {
		gated := &gatedCloseConn{
			closeEntered: make(chan struct{}),
			releaseClose: make(chan struct{}),
			closeDone:    make(chan struct{}),
		}
		wrapped := wrapDeadlineConn(gated).(*deadlineConn)
		require.NoError(t, wrapped.SetReadDeadline(time.Now().Add(-time.Millisecond)))
		<-gated.closeEntered

		closeReturned := make(chan struct{})
		go func() {
			_ = wrapped.Close()
			close(closeReturned)
		}()
		select {
		case <-closeReturned:
			t.Fatal("Close returned too early")
		case <-time.After(50 * time.Millisecond):
		}
		close(gated.releaseClose)
		select {
		case <-closeReturned:
		case <-time.After(time.Second):
			t.Fatal("Close did not return")
		}
		assert.Equal(t, int32(1), gated.closeCount.Load())
	})
}

// TestProxyChain 覆盖层校验、直连与级联关闭
func TestProxyChain(t *testing.T) {
	cases := []struct {
		name  string
		layer Layer
		want  string
	}{
		{name: "emptyType", layer: Layer{Name: "jump", Host: "h", Port: 22}, want: "empty type"},
		{name: "badPort", layer: Layer{Type: SSHX_LAYER_SSH, Host: "h", Port: 0, SSHConfig: &ssh.ClientConfig{}}, want: "invalid port"},
		{name: "missingConfig", layer: Layer{Type: SSHX_LAYER_SSH, Host: "h", Port: 22}, want: "missing SSHConfig"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateLayer(0, testCase.layer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.want)
		})
	}

	called := false
	chain := Chain{Direct: DialFunc(func(ctx context.Context, addr string) (net.Conn, error) {
		called = true
		left, right := net.Pipe()
		_ = right.Close()
		return left, nil
	})}
	conn, err := chain.Dial(context.Background(), "target:443")
	require.NoError(t, err)
	assert.True(t, called)
	require.NoError(t, conn.Close())

	_, err = Chain{Layers: []Layer{{Type: "socks5", Host: "h", Port: 1}}}.Dial(context.Background(), "x:1")
	assert.ErrorIs(t, err, ErrUnsupportedLayer)

	closer1, closer2 := &recordingCloser{}, &recordingCloser{}
	_, right := net.Pipe()
	wrapped := &connWithClosers{Conn: right, closers: []io.Closer{closer1, closer2}}
	require.NoError(t, wrapped.Close())
	assert.Equal(t, int32(1), closer1.closes.Load())
	require.NoError(t, wrapped.Close())
	assert.Equal(t, int32(1), closer1.closes.Load())
}

// TestSFTPHelpers 覆盖临时路径与文件已存在判定
func TestSFTPHelpers(t *testing.T) {
	assert.True(t, isSFTPFileExists(os.ErrExist))
	assert.True(t, isSFTPFileExists(&sftp.StatusError{Code: 11}))
	assert.False(t, isSFTPFileExists(errors.New("other")))

	path1, err := buildSFTPTempPath("/tmp/dst.txt")
	require.NoError(t, err)
	path2, err := buildSFTPTempPath("/tmp/dst.txt")
	require.NoError(t, err)
	assert.NotEqual(t, path1, path2)
}

// TestPool 覆盖连接池关闭、拨号、singleflight、lease 与空闲回收
func TestPool(t *testing.T) {
	t.Run("ClosedAndDialError", func(t *testing.T) {
		pool := NewPool(ClientDialFunc(func(ctx context.Context, key string) (*ssh.Client, []io.Closer, error) {
			return nil, nil, errors.New("dial boom")
		}), PoolOptions{})
		pool.Close()
		pool.Close()
		_, err := pool.Get(context.Background(), "k1")
		assert.ErrorIs(t, err, ErrClosed)

		pool = NewPool(ClientDialFunc(func(ctx context.Context, key string) (*ssh.Client, []io.Closer, error) {
			return nil, nil, errors.New("dial boom")
		}), PoolOptions{})
		defer pool.Close()
		_, err = pool.Get(context.Background(), "k1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sshx: dial")
	})

	t.Run("RejectNilClient", func(t *testing.T) {
		closer := &recordingCloser{}
		pool := NewPool(ClientDialFunc(func(ctx context.Context, key string) (*ssh.Client, []io.Closer, error) {
			return nil, []io.Closer{closer}, nil
		}), PoolOptions{})
		defer pool.Close()
		_, err := pool.Get(context.Background(), "k1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil client")
		assert.Equal(t, int32(1), closer.closes.Load())
	})

	t.Run("CloseDuringBlockedDial", func(t *testing.T) {
		started := make(chan struct{})
		pool := NewPool(ClientDialFunc(func(ctx context.Context, key string) (*ssh.Client, []io.Closer, error) {
			close(started)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		}), PoolOptions{})
		errCh := make(chan error, 1)
		go func() {
			_, err := pool.Get(context.Background(), "k1")
			errCh <- err
		}()
		<-started
		pool.Close()
		select {
		case err := <-errCh:
			assert.ErrorIs(t, err, ErrClosed)
		case <-time.After(time.Second):
			t.Fatal("Get did not return after Close")
		}
	})

	t.Run("CloseWakesWaiters", func(t *testing.T) {
		started := make(chan struct{})
		pool := NewPool(ClientDialFunc(func(ctx context.Context, key string) (*ssh.Client, []io.Closer, error) {
			close(started)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		}), PoolOptions{})
		const waiters = 8
		errCh := make(chan error, waiters+1)
		go func() {
			_, err := pool.Get(context.Background(), "k1")
			errCh <- err
		}()
		<-started
		for range waiters {
			go func() {
				_, err := pool.Get(context.Background(), "k1")
				errCh <- err
			}()
		}
		time.Sleep(20 * time.Millisecond)
		pool.Close()
		for range waiters + 1 {
			select {
			case err := <-errCh:
				assert.ErrorIs(t, err, ErrClosed)
			case <-time.After(time.Second):
				t.Fatal("waiter did not wake")
			}
		}
	})

	t.Run("Singleflight", func(t *testing.T) {
		var dials atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		pool := NewPool(ClientDialFunc(func(ctx context.Context, key string) (*ssh.Client, []io.Closer, error) {
			if dials.Add(1) == 1 {
				close(started)
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-release:
			}
			return nil, nil, errors.New("boom")
		}), PoolOptions{})
		defer pool.Close()
		var waitGroup sync.WaitGroup
		for range 8 {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				_, _ = pool.Get(context.Background(), "same")
			}()
		}
		<-started
		time.Sleep(20 * time.Millisecond)
		close(release)
		waitGroup.Wait()
		assert.Equal(t, int32(1), dials.Load())
	})

	t.Run("LeaseAndIdleCleanup", func(t *testing.T) {
		pool := NewPool(ClientDialFunc(func(ctx context.Context, key string) (*ssh.Client, []io.Closer, error) {
			return nil, nil, errors.New("unused")
		}), PoolOptions{IdleTimeout: 10 * time.Millisecond})
		defer pool.Close()

		oldEntry := &poolEntry{key: "k1", generation: 1, refCount: 1, lastUsed: time.Now()}
		newEntry := &poolEntry{key: "k1", generation: 2, refCount: 1, lastUsed: time.Now()}
		pool.mu.Lock()
		pool.entries["k1"] = newEntry
		pool.mu.Unlock()
		oldEntry.mu.Lock()
		oldEntry.closed = true
		oldEntry.mu.Unlock()
		lease := &Lease{pool: pool, entry: oldEntry, generation: 1}
		lease.Release()
		lease.Release()
		newEntry.mu.Lock()
		assert.Equal(t, 1, newEntry.refCount)
		newEntry.mu.Unlock()

		closer := &recordingCloser{}
		idle := &poolEntry{
			key:        "idle",
			generation: 1,
			lastUsed:   time.Now().Add(-time.Hour),
			closers:    []io.Closer{closer},
		}
		pool.mu.Lock()
		pool.entries["idle"] = idle
		pool.mu.Unlock()
		pool.cleanupIdle()
		pool.mu.Lock()
		_, ok := pool.entries["idle"]
		pool.mu.Unlock()
		assert.False(t, ok)
		assert.Equal(t, int32(1), closer.closes.Load())
	})
}
