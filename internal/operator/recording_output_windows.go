//go:build windows

package operator

import "golang.org/x/sys/windows"

func replaceRecordingOutput(partPath, finalPath string) error {
	return windows.Rename(partPath, finalPath)
}
