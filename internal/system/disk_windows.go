//go:build windows

package system

import (
	"syscall"
	"unsafe"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

func GetDiskUsage(path string) (DiskUsage, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return DiskUsage{}, err
	}
	var freeToCaller, total, free uint64
	r1, _, err := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&free)),
	)
	if r1 == 0 {
		return DiskUsage{}, err
	}
	return DiskUsage{
		Size:  total,
		Used:  total - free,
		Avail: freeToCaller,
	}, nil
}
