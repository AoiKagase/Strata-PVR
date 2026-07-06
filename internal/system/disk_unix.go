//go:build !windows

package system

import "syscall"

func GetDiskUsage(path string) (DiskUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return DiskUsage{}, err
	}
	size := stat.Blocks * uint64(stat.Bsize)
	avail := stat.Bavail * uint64(stat.Bsize)
	return DiskUsage{
		Size:  size,
		Used:  size - avail,
		Avail: avail,
	}, nil
}
