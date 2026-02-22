// Package metrics collects host resource utilization for heartbeat reporting.
//
// Currently returns zero values — a full implementation using gopsutil
// is planned for a future step. The package exists so the connection manager
// can call CollectMetrics() without needing to change its import graph later.
//
// TODO: implement with github.com/shirou/gopsutil/v3 when adding monitoring.
package metrics

import proto "github.com/arkeep-io/arkeep/shared/proto"

// Collect returns a snapshot of current host resource usage.
// Values are percentages (0–100). Returns zeros until gopsutil is wired in.
func Collect() *proto.SystemMetrics {
	return &proto.SystemMetrics{
		CpuPercent:  0,
		MemPercent:  0,
		DiskPercent: 0,
	}
}