//go:build windows

package webdav

import (
	"errors"
	"golang.org/x/sys/windows"
)

func diskAvailable(path string) (int64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, errors.New("临时目录路径无效")
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(name, &available, nil, nil); err != nil {
		return 0, errors.New("无法检查临时目录磁盘空间")
	}
	return int64(available), nil
}
