// Package scheduler 封装 robfig/cron 定时任务调度，支持优雅退出
package scheduler

import (
	"context"
	"fmt"

	"github.com/robfig/cron/v3"
)

// Job 定时任务接口
type Job interface {
	Run(ctx context.Context)
}

// Scheduler 定时任务调度器
type Scheduler struct {
	cron *cron.Cron
}

// New 创建调度器，默认启用秒级字段（6 字段表达式）
func New() *Scheduler {
	return &Scheduler{cron: cron.New(cron.WithSeconds())}
}

// AddFunc 注册定时任务，spec 为 cron 表达式
func (s *Scheduler) AddFunc(spec string, cmd func()) error {
	_, err := s.cron.AddFunc(spec, cmd)
	if err != nil {
		return fmt.Errorf("scheduler add func %q: %w", spec, err)
	}
	return nil
}

// AddJob 注册定时任务（实现 Job 接口），ctx 会透传给 Job.Run
func (s *Scheduler) AddJob(ctx context.Context, spec string, job Job) error {
	return s.AddFunc(spec, func() { job.Run(ctx) })
}

// Start 启动调度
func (s *Scheduler) Start() {
	s.cron.Start()
}

// StopWithContext 收到 ctx 取消信号时停止调度（优雅退出）
func (s *Scheduler) StopWithContext(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = s.cron.Stop()
	}()
}
