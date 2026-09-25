// Package visor pkg/visor/api_host_stats.go c3-vis-core
//
// HostStats — psutil-style resource snapshot of the host the visor
// is running on. Backs the hypervisor UI's "Resource Monitor"
// panel; the data shape is meant to be polled at ~1s and graphed.
//
// Counter-style fields (network bytes, GC count, …) are cumulative
// since boot or process start; the client diffs across polls to
// derive a rate. Gauge-style fields (CPU%, mem%, RSS) are absolute.
//
// The first call to cpu.Percent(0, …) returns 0 because there's no
// previous sample to compare against; that's fine — the second
// call returns a real value, and 1s polling means the "0 on first
// reading" only shows up briefly during initial paint.
package visor

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	gnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// collectLoadStats gathers a LoadStats snapshot. Every field degrades
// independently: a metric whose syscall fails (e.g. load.Avg on Windows) is
// left at zero rather than failing the whole snapshot.
func collectLoadStats() *visorapi.LoadStats {
	ls := &visorapi.LoadStats{}
	if avg, err := load.Avg(); err == nil && avg != nil {
		ls.Load1, ls.Load5, ls.Load15 = avg.Load1, avg.Load5, avg.Load15
	}
	if n, err := cpu.Counts(true); err == nil {
		ls.CPUCores = n
	}
	if vm, err := mem.VirtualMemory(); err == nil && vm != nil {
		ls.MemUsedPercent = vm.UsedPercent
	}
	if du, err := disk.Usage("/"); err == nil && du != nil {
		ls.DiskUsedPercent = du.UsedPercent
	}
	return ls
}

// HostStats implements API. Best-effort: a probe failure on one
// subsystem (e.g., disk usage on an unusual mount layout) doesn't
// take down the whole call — that field is left at its zero value.
func (v *Visor) HostStats() (*visorapi.HostStatsInfo, error) {
	out := &visorapi.HostStatsInfo{}

	// Host identity
	if h, err := host.Info(); err == nil && h != nil {
		out.Hostname = h.Hostname
		out.OS = h.OS
		out.Platform = h.Platform
		out.Arch = runtime.GOARCH
		out.UptimeSeconds = h.Uptime
	} else {
		out.Arch = runtime.GOARCH
	}

	// CPU — first call returns 0 (no baseline); subsequent calls
	// return % since previous call. UI's first sample will read
	// zero, real value lands on the second poll.
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		out.CPUPercent = pcts[0]
	}
	if n, err := cpu.Counts(false); err == nil {
		out.CPUCount = n
	}
	if n, err := cpu.Counts(true); err == nil {
		out.CPULogicalCount = n
	}

	// Memory
	if vm, err := mem.VirtualMemory(); err == nil && vm != nil {
		out.MemTotal = vm.Total
		out.MemUsed = vm.Used
		out.MemAvailable = vm.Available
		out.MemPercent = vm.UsedPercent
	}
	if sm, err := mem.SwapMemory(); err == nil && sm != nil {
		out.SwapTotal = sm.Total
		out.SwapUsed = sm.Used
		out.SwapPercent = sm.UsedPercent
	}

	// Disk (root)
	if du, err := disk.Usage("/"); err == nil && du != nil {
		out.DiskTotal = du.Total
		out.DiskUsed = du.Used
		out.DiskFree = du.Free
		out.DiskPercent = du.UsedPercent
	}

	// Network — sum across interfaces. pernic=false yields one row
	// of cumulative totals.
	if ns, err := gnet.IOCounters(false); err == nil && len(ns) > 0 {
		out.NetBytesSent = ns[0].BytesSent
		out.NetBytesRecv = ns[0].BytesRecv
		out.NetPacketsSent = ns[0].PacketsSent
		out.NetPacketsRecv = ns[0].PacketsRecv
	}

	// Visor process
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil && p != nil { //nolint:gosec // pid fits
		ps := &visorapi.ProcessStatsInfo{PID: p.Pid}
		if cp, err := p.CPUPercent(); err == nil {
			ps.CPUPercent = cp
		}
		if mi, err := p.MemoryInfo(); err == nil && mi != nil {
			ps.MemRSS = mi.RSS
			ps.MemVMS = mi.VMS
		}
		if nt, err := p.NumThreads(); err == nil {
			ps.NumThreads = nt
		}
		if nfd, err := p.NumFDs(); err == nil {
			ps.NumFDs = nfd
		}
		if ct, err := p.CreateTime(); err == nil {
			ps.StartTimeMS = ct
		}
		if conns, err := p.Connections(); err == nil {
			ps.OpenConns = len(conns)
		}
		out.Process = ps
	}

	return out, nil
}

// formatHostStatsErrors keeps a placeholder for future error
// aggregation. Currently we swallow per-subsystem errors silently
// because partial data is more useful than a total failure on a
// quirky system; if something is consistently zero in the UI, that's
// a flag for the user to investigate the host.
//
//nolint:unused
func formatHostStatsErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("host stats: %d subsystem errors (first: %w)", len(errs), errs[0])
}

// secondsAgo helps the UI render uptime cleanly when StartTimeMS is
// available; kept as a small helper rather than embedded so the
// formatting choice stays on the client.
//
//nolint:unused
func secondsAgo(unixMS int64) int64 {
	if unixMS <= 0 {
		return 0
	}
	return time.Now().UnixMilli()/1 - unixMS/1
}
