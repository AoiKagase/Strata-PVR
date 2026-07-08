//go:build !windows && !linux

package system

import "errors"

var errSystemUsageUnsupported = errors.New("system usage is unsupported on this platform")

func GetCPUTimes() (CPUTimes, error) {
	return CPUTimes{}, errSystemUsageUnsupported
}

func GetMemoryUsage() (MemoryUsage, error) {
	return MemoryUsage{}, errSystemUsageUnsupported
}
