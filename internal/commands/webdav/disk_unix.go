//go:build linux || darwin

package webdav

import (
	"errors"
	"syscall"
)

func diskAvailable(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, errors.New("无法检查临时目录磁盘空间")
	}
	// Reject Linux tmpfs regardless of where the deployment directory was mounted.
	if uint64(stat.Type) == 0x01021994 {
		return 0, errors.New("WebDAV 临时目录不能位于 tmpfs，请将部署目录放在磁盘")
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
