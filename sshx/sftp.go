package sshx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/ethereal3x/apc/logger"
	"github.com/pkg/sftp"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// WithSFTPParams WithSFTP 参数
type WithSFTPParams struct {
	Ctx    context.Context
	Client *ssh.Client
	Fn     func(*sftp.Client) error
}

// WithSFTP 在已有 SSH 客户端上创建 SFTP 会话并执行 fn
//
// ctx 取消时关闭 sftp/SSH 打断阻塞；Fn 返回后同步停止 cancel watcher，再检查 ctx.Err
func WithSFTP(params WithSFTPParams) error {
	if params.Ctx == nil {
		return fmt.Errorf("sshx: with sftp: nil context")
	}
	if params.Client == nil {
		return fmt.Errorf("sshx: with sftp: nil client")
	}
	if params.Fn == nil {
		return fmt.Errorf("sshx: with sftp: nil fn")
	}
	if err := params.Ctx.Err(); err != nil {
		return fmt.Errorf("sshx: with sftp: %w", err)
	}

	sftpClient, err := newSFTPClient(newSFTPClientParams{Ctx: params.Ctx, Client: params.Client})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := sftpClient.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close sftp client", zap.Error(closeErr))
		}
	}()

	stopWatch := CloseOnCancel(params.Ctx, sftpClient, params.Client)
	fnErr := params.Fn(sftpClient)
	stopWatch()

	if params.Ctx.Err() != nil {
		return fmt.Errorf("sshx: sftp operation: %w", params.Ctx.Err())
	}
	if fnErr != nil {
		return fmt.Errorf("sshx: sftp operation: %w", fnErr)
	}
	return nil
}

// CopyBetweenParams CopyBetween 参数
type CopyBetweenParams struct {
	Ctx       context.Context
	SrcClient *ssh.Client
	SrcPath   string
	DstClient *ssh.Client
	DstPath   string
}

// CopyBetween 在两个 SSH 客户端之间流式传输文件（SFTP 直传，不经本地磁盘）
//
// 写入同目录临时文件，Close 成功后再原子替换目标
// 取消时关闭底层 SFTP/SSH 打断网络请求；原目标文件保持完整
// 连接仍可用时尽力清理临时文件；网络已断时允许遗留并记日志
func CopyBetween(params CopyBetweenParams) error {
	if params.Ctx == nil {
		return fmt.Errorf("sshx: copy between: nil context")
	}
	if params.SrcClient == nil || params.DstClient == nil {
		return fmt.Errorf("sshx: copy between: nil client")
	}
	if err := params.Ctx.Err(); err != nil {
		return fmt.Errorf("sshx: copy between: %w", err)
	}

	srcSFTP, err := newSFTPClient(newSFTPClientParams{Ctx: params.Ctx, Client: params.SrcClient})
	if err != nil {
		return fmt.Errorf("sshx: source sftp: %w", err)
	}
	defer func() {
		if closeErr := srcSFTP.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close source sftp client", zap.Error(closeErr))
		}
	}()

	dstSFTP, err := newSFTPClient(newSFTPClientParams{Ctx: params.Ctx, Client: params.DstClient})
	if err != nil {
		return fmt.Errorf("sshx: destination sftp: %w", err)
	}
	defer func() {
		if closeErr := dstSFTP.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close destination sftp client", zap.Error(closeErr))
		}
	}()

	return copyBetweenSFTP(copyBetweenSFTPParams{
		Ctx:       params.Ctx,
		SrcSFTP:   srcSFTP,
		DstSFTP:   dstSFTP,
		SrcClient: params.SrcClient,
		DstClient: params.DstClient,
		SrcPath:   params.SrcPath,
		DstPath:   params.DstPath,
	})
}

// newSFTPClientParams newSFTPClient 参数
type newSFTPClientParams struct {
	Ctx    context.Context
	Client *ssh.Client
}

// newSFTPClient 在 goroutine 中创建 SFTP，ctx 取消时关闭 SSH client 打断初始化
func newSFTPClient(params newSFTPClientParams) (*sftp.Client, error) {
	type createResult struct {
		client *sftp.Client
		err    error
	}
	done := make(chan createResult, 1)
	go func() {
		client, err := sftp.NewClient(params.Client)
		done <- createResult{client: client, err: err}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			return nil, fmt.Errorf("sshx: create sftp client: %w", result.err)
		}
		if err := params.Ctx.Err(); err != nil {
			if result.client != nil {
				if closeErr := result.client.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
					logger.Warn("close sftp client after create cancel race", zap.Error(closeErr))
				}
			}
			return nil, fmt.Errorf("sshx: create sftp client: %w", err)
		}
		return result.client, nil
	case <-params.Ctx.Done():
		if closeErr := params.Client.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close ssh client during sftp init cancel", zap.Error(closeErr))
		}
		result := <-done
		if result.client != nil {
			if closeErr := result.client.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
				logger.Warn("close sftp client created after cancel", zap.Error(closeErr))
			}
		}
		return nil, fmt.Errorf("sshx: create sftp client: %w", params.Ctx.Err())
	}
}

// copyBetweenSFTPParams copyBetweenSFTP 参数
type copyBetweenSFTPParams struct {
	Ctx       context.Context
	SrcSFTP   *sftp.Client
	DstSFTP   *sftp.Client
	SrcClient *ssh.Client
	DstClient *ssh.Client
	SrcPath   string
	DstPath   string
}

// copyBetweenSFTP 执行临时文件写入与原子替换，完整流程受 ctx 控制
func copyBetweenSFTP(params copyBetweenSFTPParams) error {
	if err := params.Ctx.Err(); err != nil {
		return fmt.Errorf("sshx: copy between: %w", err)
	}

	stopWatch := CloseOnCancel(params.Ctx, params.SrcSFTP, params.DstSFTP, params.SrcClient, params.DstClient)
	opErr := runCopyBetweenSFTP(params)
	stopWatch()

	if params.Ctx.Err() != nil {
		return fmt.Errorf("sshx: file transfer %q -> %q: %w", params.SrcPath, params.DstPath, params.Ctx.Err())
	}
	return opErr
}

// runCopyBetweenSFTP 在取消 watcher 已启动的前提下执行打开、复制与原子替换
func runCopyBetweenSFTP(params copyBetweenSFTPParams) error {
	srcFile, err := params.SrcSFTP.Open(params.SrcPath)
	if err != nil {
		return fmt.Errorf("sshx: open source file %q: %w", params.SrcPath, err)
	}
	defer func() {
		if closeErr := srcFile.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close source file", zap.String("path", params.SrcPath), zap.Error(closeErr))
		}
	}()

	tmpPath, dstFile, err := createExclusiveSFTPTemp(params.DstSFTP, params.DstPath)
	if err != nil {
		return err
	}

	cleanupTemp := true
	defer func() {
		if !cleanupTemp {
			return
		}
		if removeErr := params.DstSFTP.Remove(tmpPath); removeErr != nil && !IsExpectedCloseErr(removeErr) {
			logger.Warn("remove temp file", zap.String("path", tmpPath), zap.Error(removeErr))
		}
	}()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		if closeErr := dstFile.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close temp file after copy failure", zap.String("path", tmpPath), zap.Error(closeErr))
		}
		if params.Ctx.Err() != nil {
			return fmt.Errorf("sshx: file transfer %q -> %q: %w", params.SrcPath, params.DstPath, params.Ctx.Err())
		}
		return fmt.Errorf("sshx: file transfer %q -> %q: %w", params.SrcPath, params.DstPath, err)
	}
	if err := params.Ctx.Err(); err != nil {
		if closeErr := dstFile.Close(); closeErr != nil && !IsExpectedCloseErr(closeErr) {
			logger.Warn("close temp file after cancel", zap.String("path", tmpPath), zap.Error(closeErr))
		}
		return fmt.Errorf("sshx: file transfer %q -> %q: %w", params.SrcPath, params.DstPath, err)
	}
	if err := dstFile.Close(); err != nil {
		return fmt.Errorf("sshx: close temp file %q: %w", tmpPath, err)
	}
	if err := atomicReplaceSFTPFile(params.DstSFTP, tmpPath, params.DstPath); err != nil {
		return err
	}
	cleanupTemp = false
	return nil
}

// createExclusiveSFTPTemp 使用随机名与 O_EXCL 创建同目录临时文件，避免并发碰撞
// 仅在临时文件已存在时重试；其他错误立即返回并保留原始错误链
func createExclusiveSFTPTemp(dstSFTP *sftp.Client, dstPath string) (string, *sftp.File, error) {
	const maxAttempts = 8
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		tmpPath, err := buildSFTPTempPath(dstPath)
		if err != nil {
			return "", nil, err
		}
		file, err := dstSFTP.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if err == nil {
			return tmpPath, file, nil
		}
		lastErr = err
		if !isSFTPFileExists(err) {
			return "", nil, fmt.Errorf("sshx: create exclusive temp file for %q: %w", dstPath, err)
		}
	}
	return "", nil, fmt.Errorf("sshx: create exclusive temp file for %q: %w", dstPath, lastErr)
}

// isSFTPFileExists 判断错误是否表示目标文件已存在
func isSFTPFileExists(err error) bool {
	if errors.Is(err, os.ErrExist) {
		return true
	}
	var status *sftp.StatusError
	// SSH_FX_FILE_ALREADY_EXISTS = 11
	if errors.As(err, &status) && status.Code == 11 {
		return true
	}
	return false
}

// atomicReplaceSFTPFile 优先 posix-rename 原子替换，失败时再尝试普通 Rename 且不先删目标
func atomicReplaceSFTPFile(dstSFTP *sftp.Client, tmpPath, dstPath string) error {
	if err := dstSFTP.PosixRename(tmpPath, dstPath); err == nil {
		return nil
	}
	if err := dstSFTP.Rename(tmpPath, dstPath); err != nil {
		return fmt.Errorf("sshx: rename temp file %q -> %q: %w", tmpPath, dstPath, err)
	}
	return nil
}

// buildSFTPTempPath 在目标同目录生成带加密随机后缀的临时文件路径
func buildSFTPTempPath(dstPath string) (string, error) {
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", fmt.Errorf("sshx: generate temp file suffix: %w", err)
	}
	dir := path.Dir(dstPath)
	base := path.Base(dstPath)
	return path.Join(dir, fmt.Sprintf(".%s.sshx.%s.tmp", base, hex.EncodeToString(randomBytes[:]))), nil
}
