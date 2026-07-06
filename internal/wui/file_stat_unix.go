//go:build linux || darwin || freebsd || netbsd || openbsd

package wui

import (
	"os"
	"syscall"
)

func enrichFileStatJSON(value map[string]any, info os.FileInfo) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	value["dev"] = uint64(stat.Dev)
	value["ino"] = uint64(stat.Ino)
	value["mode"] = uint32(stat.Mode)
	value["uid"] = uint32(stat.Uid)
	value["gid"] = uint32(stat.Gid)
	value["rdev"] = uint64(stat.Rdev)
	value["size"] = stat.Size
	value["blocks"] = stat.Blocks
	value["blksize"] = stat.Blksize
}
