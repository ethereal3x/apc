package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestAddFuncAndStart 校验注册并触发定时任务
func TestAddFuncAndStart(t *testing.T) {
	s := New()
	var counter int32
	if err := s.AddFunc("* * * * * *", func() {
		atomic.AddInt32(&counter, 1)
	}); err != nil {
		t.Fatalf("AddFunc failed: %v", err)
	}
	s.Start()
	defer s.cron.Stop()

	time.Sleep(2500 * time.Millisecond)
	if atomic.LoadInt32(&counter) < 1 {
		t.Fatalf("expected job to run at least once, got %d", counter)
	}
}

// TestAddFuncInvalidSpec 校验非法 cron 表达式返回错误
func TestAddFuncInvalidSpec(t *testing.T) {
	s := New()
	if err := s.AddFunc("invalid", func() {}); err == nil {
		t.Fatal("expected error for invalid spec")
	}
}

// TestStopWithContext 校验 ctx 取消停止调度器
func TestStopWithContext(t *testing.T) {
	s := New()
	ctx, cancel := context.WithCancel(context.Background())

	s.Start()
	s.StopWithContext(ctx)

	cancel()
	time.Sleep(200 * time.Millisecond)
}

// echoJob 实现 Job 接口，记录是否被调用
type echoJob struct {
	called *int32
}

func (j *echoJob) Run(ctx context.Context) {
	atomic.AddInt32(j.called, 1)
}

// TestAddJob 校验 Job 接口注册与触发
func TestAddJob(t *testing.T) {
	s := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	called := int32(0)
	job := &echoJob{called: &called}
	if err := s.AddJob(ctx, "* * * * * *", job); err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	s.Start()
	defer s.cron.Stop()

	time.Sleep(2500 * time.Millisecond)
	if atomic.LoadInt32(&called) < 1 {
		t.Fatalf("expected job.Run to be called at least once, got %d", called)
	}
}
