package tool

import (
	"errors"
	"sync"
	"time"
)

const (
	// snowflakeEpoch Snowflake 起始时间戳（毫秒）
	snowflakeEpoch = 1704067200000 // 2024-01-01 00:00:00 UTC
	// snowflakeWorkerIDBits 机器 ID 占用位数
	snowflakeWorkerIDBits = 10
	// snowflakeSequenceBits 序列号占用位数
	snowflakeSequenceBits = 12
	// snowflakeWorkerIDMax 最大机器 ID
	snowflakeWorkerIDMax = -1 ^ (-1 << snowflakeWorkerIDBits)
	// snowflakeSequenceMask 序列号掩码
	snowflakeSequenceMask = -1 ^ (-1 << snowflakeSequenceBits)
	// snowflakeWorkerIDShift 机器 ID 左移位数
	snowflakeWorkerIDShift = snowflakeSequenceBits
	// snowflakeTimestampShift 时间戳左移位数
	snowflakeTimestampShift = snowflakeSequenceBits + snowflakeWorkerIDBits
)

var (
	// ErrSnowflakeNotInitialized snowflake 未初始化
	ErrSnowflakeNotInitialized = errors.New("snowflake: not initialized, call Init first")
	// ErrSnowflakeInvalidWorkerID worker ID 超出范围
	ErrSnowflakeInvalidWorkerID = errors.New("snowflake: worker ID must be between 0 and 1023")
)

// snowflakeState snowflake 生成器内部状态
type snowflakeState struct {
	mu       sync.Mutex
	workerID int64
	sequence int64
	lastTS    int64
}

var snowflake = &snowflakeState{}

// InitSnowflake 初始化 snowflake 生成器，workerID 范围 0-1023
func InitSnowflake(workerID int64) error {
	if workerID < 0 || workerID > snowflakeWorkerIDMax {
		return ErrSnowflakeInvalidWorkerID
	}
	snowflake.mu.Lock()
	defer snowflake.mu.Unlock()
	snowflake.workerID = workerID
	snowflake.sequence = 0
	snowflake.lastTS = 0
	return nil
}

// GenSnowflakeID 生成全局唯一 int64 ID，需先调用 InitSnowflake
func GenSnowflakeID() (int64, error) {
	snowflake.mu.Lock()
	defer snowflake.mu.Unlock()

	if snowflake.workerID < 0 {
		return 0, ErrSnowflakeNotInitialized
	}

	now := time.Now().UnixMilli() - snowflakeEpoch
	if now == snowflake.lastTS {
		snowflake.sequence = (snowflake.sequence + 1) & snowflakeSequenceMask
		if snowflake.sequence == 0 {
			now = waitNextMillis(snowflake.lastTS)
		}
	} else {
		snowflake.sequence = 0
	}
	snowflake.lastTS = now

	id := (now << snowflakeTimestampShift) |
		(snowflake.workerID << snowflakeWorkerIDShift) |
		snowflake.sequence
	return id, nil
}

// waitNextMillis 自旋等待下一毫秒
func waitNextMillis(lastTS int64) int64 {
	now := time.Now().UnixMilli() - snowflakeEpoch
	for now <= lastTS {
		now = time.Now().UnixMilli() - snowflakeEpoch
	}
	return now
}
