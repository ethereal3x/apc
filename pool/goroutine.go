package pool

import (
	"errors"
	"sync"
)

var (
	ErrClosed  = errors.New("pool: closed")
	ErrNilTask = errors.New("pool: nil task")
)

type GoroutinePool struct {
	jobs chan func()
	wg   sync.WaitGroup
	once sync.Once
	mu   sync.RWMutex

	closed bool
}

func NewGoroutinePool(workerNum int, queueSize ...int) *GoroutinePool {
	if workerNum <= 0 {
		workerNum = 1
	}

	size := workerNum
	if len(queueSize) > 0 && queueSize[0] >= 0 {
		size = queueSize[0]
	}

	p := &GoroutinePool{
		jobs: make(chan func(), size),
	}
	for i := 0; i < workerNum; i++ {
		go p.run()
	}
	return p
}

func (p *GoroutinePool) Submit(task func()) error {
	if task == nil {
		return ErrNilTask
	}
	if p == nil {
		return ErrClosed
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}

	p.wg.Add(1)
	p.jobs <- task
	return nil
}

func (p *GoroutinePool) Wait() {
	if p == nil {
		return
	}
	p.wg.Wait()
}

func (p *GoroutinePool) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		close(p.jobs)
		p.mu.Unlock()
	})
}

func (p *GoroutinePool) run() {
	for task := range p.jobs {
		task()
		p.wg.Done()
	}
}
