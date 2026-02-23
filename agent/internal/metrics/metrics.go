// Package metrics collects host resource utilization for heartbeat reporting.
//
// It uses gopsutil to read CPU, memory, and disk usage from the host OS.
// Values are percentages in the range 0–100 and mapped to the proto.SystemMetrics
// type so the connection manager can include them in every HeartbeatRequest
// without knowing about the underlying collection mechanism.
//
// Note: on Linux, CPU percent is measured over a 100ms interval (blocking).
// This is acceptable given the heartbeat interval is 30 seconds.
package metrics

import (
	"context"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"

	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// Collect returns a snapshot of current host resource usage.
// Values are percentages (0–100). On collection error, the affected field
// is left at 0 — a partial snapshot is better than no snapshot.
func Collect() *proto.SystemMetrics {
	return &proto.SystemMetrics{
		CpuPercent:  cpuPercent(),
		MemPercent:  memPercent(),
		DiskPercent: diskPercent(),
	}
}

// cpuPercent returns the overall CPU usage percentage over a 100ms interval.
// Returns 0 on error.
func cpuPercent() float32 {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	percents, err := cpu.PercentWithContext(ctx, 100*time.Millisecond, false)
	if err != nil || len(percents) == 0 {
		return 0
	}
	return float32(percents[0])
}

// memPercent returns the percentage of RAM currently in use.
// Returns 0 on error.
func memPercent() float32 {
	v, err := mem.VirtualMemory()
	if err != nil {
		return 0
	}
	return float32(v.UsedPercent)
}

// diskPercent returns the usage percentage of the primary partition.
// Uses "/" on Unix and "C:\" on Windows.
// Returns 0 on error.
func diskPercent() float32 {
	path := "/"
	if runtime.GOOS == "windows" {
		path = `C:\`
	}
	usage, err := disk.Usage(path)
	if err != nil {
		return 0
	}
	return float32(usage.UsedPercent)
}