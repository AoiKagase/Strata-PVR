//go:build !windows

package operator

import "os"

func replaceRecordingOutput(partPath, finalPath string) error {
	return os.Rename(partPath, finalPath)
}
