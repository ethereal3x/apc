package cache

import (
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestRedisLock(t *testing.T) {
	// 初始化 Redis 客户端
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 创建分布式锁实例
	lock := NewRedisLock(client, "test_lock", 3*time.Second)

	// 尝试获取锁并执行任务
	t.Run("Lock acquired successfully", func(t *testing.T) {
		err := lock.TryLock(func() error {
			// 执行需要分布式锁保护的操作
			fmt.Println("Lock acquired, performing task...")
			time.Sleep(2 * time.Second) // 模拟长时间任务
			fmt.Println("Task completed.")
			return nil
		})
		assert.Nil(t, err, "Should not return error when lock is acquired")
	})

	// 尝试获取锁并执行任务
	t.Run("Lock acquisition failed", func(t *testing.T) {
		// 获取另一个锁实例
		lock2 := NewRedisLock(client, "test_lock", 3*time.Second)
		err := lock2.TryLock(func() error {
			t.Errorf("Should not acquire lock when it's already held")
			return nil
		})
		assert.NotNil(t, err, "Should return error when lock acquisition fails")
	})

	// 手动释放锁
	t.Run("Lock released", func(t *testing.T) {
		err := lock.TryLock(func() error {
			// 执行需要分布式锁保护的操作
			fmt.Println("Lock acquired, performing task...")
			time.Sleep(1 * time.Second) // 模拟长时间任务
			fmt.Println("Task completed.")
			return nil
		})
		assert.Nil(t, err, "Should not return error when lock is acquired again")
	})
}
