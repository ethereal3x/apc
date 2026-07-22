package sshx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereal3x/apc/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// ErrClosed 连接池已关闭
var ErrClosed = errors.New("sshx: pool closed")

// errEntryUnhealthy 表示缓存条目不可用，Get 应重新拨号
var errEntryUnhealthy = errors.New("sshx: pool entry unhealthy")

const (
	// SSHX_DEFAULT_HEALTH_CHECK_TIMEOUT 默认连接存活检测超时
	SSHX_DEFAULT_HEALTH_CHECK_TIMEOUT = 5 * time.Second
)

// ClientDialer 创建 SSH 客户端连接的接口，由调用方实现以解耦凭据解析、代理、跳板机等逻辑
//
// Dial 必须遵守 ctx 取消；Pool.Close 会取消拨号 context 并唤醒等待者
// 若 Dial 忽略 ctx，Close 可能阻塞到 Dial 自行返回
// 返回的 closers 是除 *ssh.Client 外需要一并管理的资源，池会在回收时统一关闭
type ClientDialer interface {
	Dial(ctx context.Context, key string) (*ssh.Client, []io.Closer, error)
}

// ClientDialFunc 适配函数签名，便于不定义类型即可注入连接池拨号逻辑
type ClientDialFunc func(ctx context.Context, key string) (*ssh.Client, []io.Closer, error)

// Dial 实现 ClientDialer
func (dialFunc ClientDialFunc) Dial(ctx context.Context, key string) (*ssh.Client, []io.Closer, error) {
	return dialFunc(ctx, key)
}

// PoolOptions 连接池配置，由调用方显式传入
type PoolOptions struct {
	// IdleTimeout 空闲回收超时。值 <= 0 时禁用空闲回收，仅 Close 时清理
	IdleTimeout time.Duration
	// HealthCheckTimeout Get 时存活检测超时。值 <= 0 使用 SSHX_DEFAULT_HEALTH_CHECK_TIMEOUT
	HealthCheckTimeout time.Duration
}

// EntryInfo 连接池条目快照，用于 UI 展示或监控
type EntryInfo struct {
	Key        string
	RefCount   int
	Generation uint64
	LastUsed   int64 // Unix timestamp
}

// Lease 表示一次连接借用，Release 绑定到具体条目 generation，重复调用安全
type Lease struct {
	Client     *ssh.Client
	pool       *Pool
	entry      *poolEntry
	generation uint64
	released   atomic.Bool
}

// Release 归还借用。若条目已被 Remove/Close 或 generation 不匹配则忽略
// 重复调用是安全的
func (lease *Lease) Release() {
	if lease == nil {
		return
	}
	if !lease.released.CompareAndSwap(false, true) {
		return
	}
	lease.pool.releaseLease(lease.entry, lease.generation)
}

type poolEntry struct {
	client      *ssh.Client
	closers     []io.Closer
	key         string
	generation  uint64
	lastUsed    time.Time
	refCount    int
	mu          sync.Mutex
	closed      bool
	healthProbe *healthProbe
}

// healthProbe 同一 entry 的共享存活探测，避免并发 Get 重复 SendRequest
type healthProbe struct {
	done  chan struct{}
	alive bool
}

// acquireLocked 在已持有 entry.mu 时增加引用计数
func (entry *poolEntry) acquireLocked() {
	entry.refCount++
	entry.lastUsed = time.Now()
}

// releaseLocked 在已持有 entry.mu 时减少引用计数，禁止降为负数
func (entry *poolEntry) releaseLocked() {
	if entry.refCount > 0 {
		entry.refCount--
	}
	entry.lastUsed = time.Now()
}

// closeEntry 关闭客户端及附加资源，幂等，网络 Close 在锁外执行
func (entry *poolEntry) closeEntry() {
	entry.mu.Lock()
	if entry.closed {
		entry.mu.Unlock()
		return
	}
	entry.closed = true
	client := entry.client
	closers := entry.closers
	entry.client = nil
	entry.closers = nil
	entry.mu.Unlock()
	closeSSHResources(client, closers)
}

// inflightDial 同 key 并发拨号合并用的 future
type inflightDial struct {
	done         chan struct{}
	completeOnce sync.Once
	entry        *poolEntry
	err          error
	generation   uint64
}

// complete 幂等完成 inflight，避免多次 close(done) panic
func (call *inflightDial) complete(entry *poolEntry, generation uint64, err error) {
	call.completeOnce.Do(func() {
		call.entry = entry
		call.generation = generation
		call.err = err
		close(call.done)
	})
}

// Pool SSH 连接池，按 key 引用计数复用 *ssh.Client
//
// 生命周期：NewPool 启动后台清理 goroutine 与 life context，Close 取消拨号并等待 goroutine 退出
// 并发安全；Get/Lease.Release/Remove/Close 可多 goroutine 同时调用
type Pool struct {
	mu                 sync.Mutex
	entries            map[string]*poolEntry
	inflight           map[string]*inflightDial
	dialer             ClientDialer
	idleTime           time.Duration
	healthCheckTimeout time.Duration
	lifeCtx            context.Context
	lifeCancel         context.CancelFunc
	done               chan struct{}
	wg                 sync.WaitGroup
	pingWg             sync.WaitGroup
	closeOnce          sync.Once
	closed             bool
	nextGeneration     atomic.Uint64
}

// NewPool 创建连接池。IdleTimeout <= 0 时不做空闲回收（仅 Close 时清理）
// 后台清理每 30 秒扫描一次
func NewPool(dialer ClientDialer, options PoolOptions) *Pool {
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	healthTimeout := options.HealthCheckTimeout
	if healthTimeout <= 0 {
		healthTimeout = SSHX_DEFAULT_HEALTH_CHECK_TIMEOUT
	}
	pool := &Pool{
		entries:            make(map[string]*poolEntry),
		inflight:           make(map[string]*inflightDial),
		dialer:             dialer,
		idleTime:           options.IdleTimeout,
		healthCheckTimeout: healthTimeout,
		lifeCtx:            lifeCtx,
		lifeCancel:         lifeCancel,
		done:               make(chan struct{}),
	}
	pool.wg.Add(1)
	go pool.cleanupLoop()
	return pool
}

// Get 获取或创建一个 SSH 连接借用。调用方用完后必须调用 Lease.Release
//
// 同 key 并发拨号会合并为一次握手。缓存连接若已死会自动移除并重新拨号
// 拨号完成时若池已关闭，会完整关闭新连接并返回 ErrClosed
func (pool *Pool) Get(ctx context.Context, key string) (*Lease, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("sshx: get %q: %w", key, err)
		}
		if pool.isClosed() {
			return nil, ErrClosed
		}

		pool.mu.Lock()
		// connect exist
		if entry, ok := pool.entries[key]; ok {
			generation := entry.generation
			pool.mu.Unlock()
			if err := pool.waitHealthy(ctx, entry, generation); err != nil {
				if pool.isClosed() || errors.Is(err, ErrClosed) {
					return nil, ErrClosed
				}
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil, fmt.Errorf("sshx: get %q: %w", key, err)
				}
				continue
			}
			lease, ok := pool.tryAcquire(key, generation)
			if ok {
				return lease, nil
			}
			continue
		}

		// connect dealing
		if call, ok := pool.inflight[key]; ok {
			pool.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("sshx: get %q: %w", key, ctx.Err())
			case <-pool.lifeCtx.Done():
				return nil, ErrClosed
			case <-call.done:
				if call.err != nil {
					return nil, call.err
				}
				lease, ok := pool.tryAcquire(key, call.generation)
				if ok {
					return lease, nil
				}
				continue
			}
		}

		call := &inflightDial{done: make(chan struct{})}
		pool.inflight[key] = call
		pool.mu.Unlock()

		lease, err := pool.finishDial(ctx, key, call)
		if err != nil {
			return nil, err
		}
		return lease, nil
	}
}

// finishDial 执行一次拨号，将结果写入池或在失败/关闭时完整回收资源
func (pool *Pool) finishDial(ctx context.Context, key string, call *inflightDial) (*Lease, error) {
	dialCtx, cancelDial := mergeContexts(ctx, pool.lifeCtx)
	defer cancelDial()

	client, closers, err := pool.dialer.Dial(dialCtx, key)
	if err != nil {
		closeSSHResources(client, closers)
		if pool.isClosed() || errors.Is(err, context.Canceled) && pool.lifeCtx.Err() != nil {
			pool.failInflight(key, call, ErrClosed)
			return nil, ErrClosed
		}
		wrapped := fmt.Errorf("sshx: dial %q: %w", key, err)
		pool.failInflight(key, call, wrapped)
		return nil, wrapped
	}
	if client == nil {
		closeSSHResources(nil, closers)
		wrapped := fmt.Errorf("sshx: dial %q: nil client", key)
		pool.failInflight(key, call, wrapped)
		return nil, wrapped
	}

	pool.mu.Lock()
	delete(pool.inflight, key)
	if pool.closed {
		pool.mu.Unlock()
		closeSSHResources(client, closers)
		call.complete(nil, 0, ErrClosed)
		return nil, ErrClosed
	}
	if existing, ok := pool.entries[key]; ok {
		generation := existing.generation
		call.complete(existing, generation, nil)
		pool.mu.Unlock()
		closeSSHResources(client, closers)
		lease, ok := pool.tryAcquire(key, generation)
		if !ok {
			return nil, fmt.Errorf("sshx: get %q: entry unavailable after dial race", key)
		}
		return lease, nil
	}

	generation := pool.nextGeneration.Add(1)
	entry := &poolEntry{
		client:     client,
		closers:    closers,
		key:        key,
		generation: generation,
		lastUsed:   time.Now(),
		refCount:   1,
	}
	pool.entries[key] = entry
	call.complete(entry, generation, nil)
	pool.mu.Unlock()

	return &Lease{
		Client:     client,
		pool:       pool,
		entry:      entry,
		generation: generation,
	}, nil
}

// failInflight 标记 inflight 拨号失败并唤醒等待者
func (pool *Pool) failInflight(key string, call *inflightDial, err error) {
	pool.mu.Lock()
	delete(pool.inflight, key)
	pool.mu.Unlock()
	call.complete(nil, 0, err)
}

// waitHealthy 对空闲 entry 做共享健康探测；已有活跃 Lease 时直接复用，不发起探测
// 调用方 ctx 取消不会关闭仍被借用的连接；探测启动与 Pool.Close 通过 pool.mu 同步
func (pool *Pool) waitHealthy(ctx context.Context, entry *poolEntry, generation uint64) error {
	entry.mu.Lock()
	if entry.closed || entry.generation != generation || entry.client == nil {
		entry.mu.Unlock()
		return errEntryUnhealthy
	}
	// 有活跃 Lease 时直接复用，避免探测超时误标健康或误关共享连接
	if entry.refCount > 0 {
		entry.mu.Unlock()
		return nil
	}
	key := entry.key
	entry.mu.Unlock()

	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return ErrClosed
	}
	current, ok := pool.entries[key]
	if !ok || current != entry || current.generation != generation {
		pool.mu.Unlock()
		return errEntryUnhealthy
	}
	current.mu.Lock()
	if current.closed || current.client == nil {
		current.mu.Unlock()
		pool.mu.Unlock()
		return errEntryUnhealthy
	}
	if current.refCount > 0 {
		current.mu.Unlock()
		pool.mu.Unlock()
		return nil
	}
	probe := current.healthProbe
	if probe == nil {
		probe = &healthProbe{done: make(chan struct{})}
		current.healthProbe = probe
		client := current.client
		// 在 pool.closed 可见的临界区内完成 Add，避免 Close 提前 Wait 返回
		pool.pingWg.Add(1)
		current.mu.Unlock()
		pool.mu.Unlock()
		go pool.runHealthProbe(current, probe, client, key, generation)
	} else {
		current.mu.Unlock()
		pool.mu.Unlock()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-pool.lifeCtx.Done():
		return ErrClosed
	case <-probe.done:
		if !probe.alive {
			return errEntryUnhealthy
		}
		return nil
	}
}

// runHealthProbe 对空闲连接执行一次 keepalive 探测；失败或超时则移除并关闭
func (pool *Pool) runHealthProbe(entry *poolEntry, probe *healthProbe, client *ssh.Client, key string, generation uint64) {
	defer pool.pingWg.Done()

	pingCtx, cancelPing := context.WithTimeout(pool.lifeCtx, pool.healthCheckTimeout)
	defer cancelPing()

	done := make(chan error, 1)
	// 外层已计入 pingWg，此处再 Add 时外层尚未 Done，WaitGroup 生命周期合法
	pool.pingWg.Add(1)
	go func() {
		defer pool.pingWg.Done()
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()

	// publishProbe 发布探测结果并清空槽位，禁止超时结果长期占据 healthProbe
	publishProbe := func(alive bool) {
		probe.alive = alive
		entry.mu.Lock()
		if entry.healthProbe == probe {
			entry.healthProbe = nil
		}
		entry.mu.Unlock()
		close(probe.done)
	}

	select {
	case err := <-done:
		if err != nil {
			publishProbe(false)
			pool.removeIfGeneration(key, generation)
			return
		}
		publishProbe(true)
	case <-pingCtx.Done():
		// 仅空闲 entry 会进入探测，超时一律不健康并关闭，唤醒可能阻塞的 keepalive
		publishProbe(false)
		pool.removeIfGeneration(key, generation)
		<-done
	}
}

// tryAcquire 在 generation 仍有效且无进行中健康探测时增加引用并返回 Lease
func (pool *Pool) tryAcquire(key string, generation uint64) (*Lease, bool) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed {
		return nil, false
	}
	entry, ok := pool.entries[key]
	if !ok || entry.generation != generation {
		return nil, false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.closed || entry.client == nil {
		return nil, false
	}
	// 健康探测进行中禁止新借出，避免探测超时关闭已返回的 Lease
	if entry.healthProbe != nil {
		return nil, false
	}
	entry.acquireLocked()
	return &Lease{
		Client:     entry.client,
		pool:       pool,
		entry:      entry,
		generation: generation,
	}, true
}

// releaseLease 按 generation 归还借用，避免误释放重建后的新连接
func (pool *Pool) releaseLease(entry *poolEntry, generation uint64) {
	if entry == nil {
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.closed || entry.generation != generation {
		return
	}
	entry.releaseLocked()
}

// Remove 强制移除并关闭指定 key 的连接，无论引用计数是否为 0
func (pool *Pool) Remove(key string) {
	pool.mu.Lock()
	entry, ok := pool.entries[key]
	if ok {
		delete(pool.entries, key)
	}
	pool.mu.Unlock()
	if ok {
		entry.closeEntry()
	}
}

// removeIfGeneration 仅当 generation 匹配时移除条目
func (pool *Pool) removeIfGeneration(key string, generation uint64) {
	pool.mu.Lock()
	entry, ok := pool.entries[key]
	if !ok || entry.generation != generation {
		pool.mu.Unlock()
		return
	}
	delete(pool.entries, key)
	pool.mu.Unlock()
	entry.closeEntry()
}

// List 返回所有连接池条目快照
func (pool *Pool) List() []EntryInfo {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	infos := make([]EntryInfo, 0, len(pool.entries))
	for _, entry := range pool.entries {
		entry.mu.Lock()
		infos = append(infos, EntryInfo{
			Key:        entry.key,
			RefCount:   entry.refCount,
			Generation: entry.generation,
			LastUsed:   entry.lastUsed.Unix(),
		})
		entry.mu.Unlock()
	}
	return infos
}

// Close 关闭连接池：取消所有拨号、唤醒 inflight 等待者、释放连接，幂等
// 等待池启动的 cleanup goroutine 退出；网络 Close 在池锁外执行
func (pool *Pool) Close() {
	pool.closeOnce.Do(func() {
		pool.lifeCancel()

		pool.mu.Lock()
		pool.closed = true
		close(pool.done)
		inflights := make([]*inflightDial, 0, len(pool.inflight))
		for key, call := range pool.inflight {
			inflights = append(inflights, call)
			delete(pool.inflight, key)
		}
		entries := make([]*poolEntry, 0, len(pool.entries))
		for key, entry := range pool.entries {
			entries = append(entries, entry)
			delete(pool.entries, key)
		}
		pool.mu.Unlock()

		for _, call := range inflights {
			call.complete(nil, 0, ErrClosed)
		}

		pool.wg.Wait()
		for _, entry := range entries {
			entry.closeEntry()
		}
		// 关闭连接后等待健康探测 goroutine 退出，避免遗留 keepalive
		pool.pingWg.Wait()
	})
}

// isClosed 返回池是否已关闭
func (pool *Pool) isClosed() bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.closed
}

// cleanupLoop 后台清理空闲连接
func (pool *Pool) cleanupLoop() {
	defer pool.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-pool.done:
			return
		case <-ticker.C:
			pool.cleanupIdle()
		}
	}
}

// cleanupIdle 回收空闲超时连接；idleTime <= 0 时直接跳过
func (pool *Pool) cleanupIdle() {
	if pool.idleTime <= 0 {
		return
	}
	pool.mu.Lock()
	var toClose []*poolEntry
	for key, entry := range pool.entries {
		entry.mu.Lock()
		idle := !entry.closed && entry.refCount <= 0 && time.Since(entry.lastUsed) > pool.idleTime
		entry.mu.Unlock()
		if idle {
			delete(pool.entries, key)
			toClose = append(toClose, entry)
		}
	}
	pool.mu.Unlock()

	for _, entry := range toClose {
		entry.closeEntry()
		logger.Info("closed idle connection", zap.String("key", entry.key))
	}
}

// closeSSHResources 关闭拨号产生的 client 与附加 closers
func closeSSHResources(client *ssh.Client, closers []io.Closer) {
	if client != nil {
		if err := client.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Warn("close unused ssh client", zap.Error(err))
		}
	}
	for _, closer := range closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Warn("close unused intermediate connection", zap.Error(err))
		}
	}
}

// mergeContexts 返回在 parent 或 life 任一取消时都会取消的 context
func mergeContexts(parent, life context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(life, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}
