//go:build !linux && !darwin && !windows

package webdav

import "errors"

func diskAvailable(string) (int64, error) {
	return 0, errors.New("当前平台不支持 WebDAV 磁盘余量检查")
}
