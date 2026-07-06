//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package wui

import "os"

func allocatedFileSize(info os.FileInfo) int64 {
	return info.Size()
}
