package system

type DiskUsage struct {
	Size  uint64
	Used  uint64
	Avail uint64
}

type CPUTimes struct {
	Idle  uint64
	Total uint64
}

type MemoryUsage struct {
	Total uint64
	Used  uint64
	Avail uint64
}
