//go:build windows

package system

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

var (
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

func GetCPUTimes() (CPUTimes, error) {
	var idle, kernel, user [8]byte
	r1, _, err := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle[0])),
		uintptr(unsafe.Pointer(&kernel[0])),
		uintptr(unsafe.Pointer(&user[0])),
	)
	if r1 == 0 {
		return CPUTimes{}, err
	}
	idleTicks := binary.LittleEndian.Uint64(idle[:])
	kernelTicks := binary.LittleEndian.Uint64(kernel[:])
	userTicks := binary.LittleEndian.Uint64(user[:])
	return CPUTimes{
		Idle:  idleTicks,
		Total: kernelTicks + userTicks,
	}, nil
}

func GetMemoryUsage() (MemoryUsage, error) {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	r1, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if r1 == 0 {
		return MemoryUsage{}, err
	}
	return MemoryUsage{
		Total: status.TotalPhys,
		Used:  status.TotalPhys - status.AvailPhys,
		Avail: status.AvailPhys,
	}, nil
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var _ = syscall.Errno(0)
