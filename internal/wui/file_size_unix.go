//go:build linux || darwin || freebsd || netbsd || openbsd

package wui

import (
	"os"
	"syscall"
)

func allocatedFileSize(info os.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Blocks > 0 {
		return stat.Blocks * 512
	}
	return info.Size()
}
