package pool

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrClosed  = errors.New("pool: closed")
	ErrNilTask = errors.New("pool: nil task")
)

// Executor 协程池，支持 context 取消、panic 隔离和明确的生命周期管理
type Executor struct {
	jobs chan func()

	// taskWG 跟踪所有已通过 Submit 计入的任务，Wait 等待其归零
	taskWG sync.WaitGroup

	// submitWG + submitMu 保证 Close 在关闭 closeCh 后等待正在进行的 Submit，
	// 从而安全 close(jobs) 而不会触发 "send on closed channel" panic
	submitWG sync.WaitGroup
	submitMu sync.Mutex
	closeCh  chan struct{}
	closed   bool
}

// NewExecutor 创建协程池实例，workerNum 为 worker 数量，queueSize 为任务队列长度
func NewExecutor(workerNum int, queueSize ...int) *Executor {
	if workerNum <= 0 {
		workerNum = 1
	}

	size := workerNum
	if len(queueSize) > 0 && queueSize[0] >= 0 {
		size = queueSize[0]
	}

	executor := &Executor{
		jobs:    make(chan func(), size),
		closeCh: make(chan struct{}),
	}
	for range workerNum {
		go executor.run()
	}
	return executor
}

// Submit 提交任务到协程池，ctx 取消时立即返回 ctx.Err()
func (e *Executor) Submit(ctx context.Context, task func()) error {
	if task == nil {
		return ErrNilTask
	}
	if e == nil {
		return ErrClosed
	}

	// 防止与 Close 并发：Close 设置 closed 后会等待所有进行中的 Submit 退出
	e.submitMu.Lock()
	if e.closed {
		e.submitMu.Unlock()
		return ErrClosed
	}
	e.submitWG.Add(1)
	e.submitMu.Unlock()
	defer e.submitWG.Done()

	e.taskWG.Add(1)
	select {
	case e.jobs <- task:
		return nil
	case <-e.closeCh:
		e.taskWG.Done()
		return ErrClosed
	case <-ctx.Done():
		e.taskWG.Done()
		return ctx.Err()
	}
}

// Wait 等待所有已提交任务执行完成
func (e *Executor) Wait() {
	if e == nil {
		return
	}
	e.taskWG.Wait()
}

// Close 关闭协程池，停止接收新任务并等待队列中剩余任务执行完毕
func (e *Executor) Close() {
	if e == nil {
		return
	}

	e.submitMu.Lock()
	if e.closed {
		e.submitMu.Unlock()
		return
	}
	e.closed = true
	close(e.closeCh)
	e.submitMu.Unlock()

	// 等待所有正在进行的 Submit 退出，确保不会再有发送方
	e.submitWG.Wait()
	close(e.jobs)

	// 等待所有已计入的任务完成
	e.taskWG.Wait()
}

// run 是 worker 主循环，隔离 panic 并确保 taskWG.Done 一定执行
func (e *Executor) run() {
	for task := range e.jobs {
		e.executeTask(task)
	}
}

// executeTask 执行单个任务，隔离 panic 并确保 Done 一定被调用
func (e *Executor) executeTask(task func()) {
	defer e.taskWG.Done()
	defer func() {
		_ = recover()
	}()
	task()
}
