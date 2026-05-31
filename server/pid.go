package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

var (
	pidFile = fmt.Sprintf("/data/tmp/%s.pid", filepath.Base(os.Args[0]))
)

// WritePidFile 写入当前进程 pid 文件
func WritePidFile() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pidFile), 0755); err != nil {
		return fmt.Errorf("create pid file dir: %w", err)
	}
	if err := terminateExistingPid(); err != nil {
		return fmt.Errorf("terminate existing pid: %w", err)
	}
	file, err := os.OpenFile(pidFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open pid file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	if _, err := file.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	return nil
}

// RmPidFile 删除当前进程 pid 文件
func RmPidFile() {
	_ = os.Remove(pidFile)
}

// terminateExistingPid 终止 pid 文件记录的旧进程
func terminateExistingPid() error {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read pid file: %w", err)
	}
	pidText := strings.TrimSpace(string(data))
	if pidText == "" {
		return nil
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		return fmt.Errorf("parse pid %q: %w", pidText, err)
	}
	if pid <= 0 || pid == os.Getpid() {
		return nil
	}
	if err := syscall.Kill(pid, 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("check process %d: %w", pid, err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("kill process %d: %w", pid, err)
	}
	return nil
}
