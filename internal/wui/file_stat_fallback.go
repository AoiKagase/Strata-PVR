//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package wui

import "os"

func enrichFileStatJSON(value map[string]any, info os.FileInfo) {
}
