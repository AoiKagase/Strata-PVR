//go:build linux

package system

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func GetCPUTimes() (CPUTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return CPUTimes{}, err
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return CPUTimes{}, fmt.Errorf("unexpected /proc/stat cpu line")
	}
	var values []uint64
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return CPUTimes{}, err
		}
		values = append(values, value)
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return CPUTimes{Idle: idle, Total: total}, nil
}

func GetMemoryUsage() (MemoryUsage, error) {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return MemoryUsage{}, err
	}
	unit := uint64(info.Unit)
	if unit == 0 {
		unit = 1
	}
	total := info.Totalram * unit
	avail := info.Freeram * unit
	if total < avail {
		avail = total
	}
	return MemoryUsage{
		Total: total,
		Used:  total - avail,
		Avail: avail,
	}, nil
}
