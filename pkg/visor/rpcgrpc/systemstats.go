// Package rpcgrpc provides gRPC streaming services for the visor
package rpcgrpc

import (
	"context"
	"runtime"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// SystemStatsCollector collects system stats using gopsutil
type SystemStatsCollector struct {
	lastNetStats *net.IOCountersStat
	lastNetTime  time.Time
	lastCPUTimes []cpu.TimesStat
	lastCPUTime  time.Time
}

// NewSystemStatsCollector creates a new system stats collector
func NewSystemStatsCollector() *SystemStatsCollector {
	return &SystemStatsCollector{}
}

// Collect gathers system stats and returns them as a proto message
func (c *SystemStatsCollector) Collect(ctx context.Context, includeProcesses bool, processLimit int) (*SystemStats, error) {
	stats := &SystemStats{
		TimestampNs: time.Now().UnixNano(),
	}

	// Host info
	if hostInfo, err := c.collectHostInfo(ctx); err == nil {
		stats.Host = hostInfo
	}

	// CPU usage
	if cpuStats, avg, err := c.collectCPU(ctx); err == nil {
		stats.Cpu = cpuStats
		stats.CpuAverage = avg
	}

	// Memory
	if memStat, err := c.collectMemory(ctx); err == nil {
		stats.Memory = memStat
	}

	// Swap
	if swapStat, err := c.collectSwap(ctx); err == nil {
		stats.Swap = swapStat
	}

	// Disk usage
	if diskStats, err := c.collectDisks(ctx); err == nil {
		stats.Disks = diskStats
	}

	// Network I/O
	if netStat, err := c.collectNetwork(ctx); err == nil {
		stats.Network = netStat
	}

	// Temperatures
	if temps, err := c.collectTemperatures(ctx); err == nil {
		stats.Temps = temps
	}

	// Processes (optional, more expensive)
	if includeProcesses {
		limit := processLimit
		if limit <= 0 {
			limit = 10
		}
		if procs, err := c.collectProcesses(ctx, limit); err == nil {
			stats.Processes = procs
		}
	}

	return stats, nil
}

func (c *SystemStatsCollector) collectHostInfo(ctx context.Context) (*HostInfo, error) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return nil, err
	}

	return &HostInfo{
		Hostname:        info.Hostname,
		UptimeSec:       int64(info.Uptime), //nolint:gosec // G115: uptime won't overflow int64
		Os:              info.OS,
		Platform:        info.Platform,
		PlatformVersion: info.PlatformVersion,
		KernelVersion:   info.KernelVersion,
		KernelArch:      info.KernelArch,
		NumCpus:         int32(runtime.NumCPU()), //nolint:gosec // G115: NumCPU won't overflow int32
	}, nil
}

func (c *SystemStatsCollector) collectCPU(ctx context.Context) ([]*CpuStat, float64, error) {
	// Get current CPU times
	times, err := cpu.TimesWithContext(ctx, true) // per-CPU
	if err != nil {
		return nil, 0, err
	}

	// If we have previous times, calculate percentages
	var cpuStats []*CpuStat
	var totalUsage float64

	if c.lastCPUTimes != nil && len(c.lastCPUTimes) == len(times) {
		elapsed := time.Since(c.lastCPUTime).Seconds()
		if elapsed > 0 {
			for i, t := range times {
				last := c.lastCPUTimes[i]

				// Calculate total time delta
				totalDelta := (t.User + t.System + t.Nice + t.Idle + t.Iowait + t.Irq + t.Softirq + t.Steal) -
					(last.User + last.System + last.Nice + last.Idle + last.Iowait + last.Irq + last.Softirq + last.Steal)

				if totalDelta > 0 {
					// Calculate busy time delta
					busyDelta := (t.User + t.System + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal) -
						(last.User + last.System + last.Nice + last.Iowait + last.Irq + last.Softirq + last.Steal)

					usage := (busyDelta / totalDelta) * 100
					cpuStats = append(cpuStats, &CpuStat{
						Cpu:          t.CPU,
						UsagePercent: usage,
					})
					totalUsage += usage
				}
			}
		}
	} else {
		// First call, use gopsutil's Percent function
		percentages, err := cpu.PercentWithContext(ctx, 0, true)
		if err == nil {
			for i, pct := range percentages {
				cpuStats = append(cpuStats, &CpuStat{
					Cpu:          times[i].CPU,
					UsagePercent: pct,
				})
				totalUsage += pct
			}
		}
	}

	// Store for next calculation
	c.lastCPUTimes = times
	c.lastCPUTime = time.Now()

	// Calculate average
	avg := 0.0
	if len(cpuStats) > 0 {
		avg = totalUsage / float64(len(cpuStats))
	}

	return cpuStats, avg, nil
}

func (c *SystemStatsCollector) collectMemory(ctx context.Context) (*MemoryStat, error) {
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, err
	}

	return &MemoryStat{
		Total:       vm.Total,
		Used:        vm.Used,
		Available:   vm.Available,
		UsedPercent: vm.UsedPercent,
	}, nil
}

func (c *SystemStatsCollector) collectSwap(ctx context.Context) (*MemoryStat, error) {
	swap, err := mem.SwapMemoryWithContext(ctx)
	if err != nil {
		return nil, err
	}

	return &MemoryStat{
		Total:       swap.Total,
		Used:        swap.Used,
		Available:   swap.Free,
		UsedPercent: swap.UsedPercent,
	}, nil
}

func (c *SystemStatsCollector) collectDisks(ctx context.Context) ([]*DiskStat, error) {
	partitions, err := disk.PartitionsWithContext(ctx, false) // physical disks only
	if err != nil {
		return nil, err
	}

	var disks []*DiskStat
	for _, p := range partitions {
		usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil {
			continue
		}

		disks = append(disks, &DiskStat{
			Device:      p.Device,
			Mountpoint:  p.Mountpoint,
			Fstype:      p.Fstype,
			Total:       usage.Total,
			Used:        usage.Used,
			Free:        usage.Free,
			UsedPercent: usage.UsedPercent,
		})
	}

	return disks, nil
}

func (c *SystemStatsCollector) collectNetwork(ctx context.Context) (*NetworkStat, error) {
	counters, err := net.IOCountersWithContext(ctx, false) // aggregated
	if err != nil || len(counters) == 0 {
		return nil, err
	}

	current := &counters[0]
	stat := &NetworkStat{
		BytesSent:   current.BytesSent,
		BytesRecv:   current.BytesRecv,
		PacketsSent: current.PacketsSent,
		PacketsRecv: current.PacketsRecv,
	}

	// Calculate rates if we have previous data
	if c.lastNetStats != nil {
		elapsed := time.Since(c.lastNetTime).Seconds()
		if elapsed > 0 {
			stat.BytesSentRate = float64(current.BytesSent-c.lastNetStats.BytesSent) / elapsed
			stat.BytesRecvRate = float64(current.BytesRecv-c.lastNetStats.BytesRecv) / elapsed
		}
	}

	// Store for next calculation
	c.lastNetStats = current
	c.lastNetTime = time.Now()

	return stat, nil
}

func (c *SystemStatsCollector) collectTemperatures(ctx context.Context) ([]*TempStat, error) {
	temps, err := host.SensorsTemperaturesWithContext(ctx)
	if err != nil {
		return nil, err
	}

	var result []*TempStat
	for _, t := range temps {
		result = append(result, &TempStat{
			SensorKey:   t.SensorKey,
			Temperature: t.Temperature,
			High:        t.High,
			Critical:    t.Critical,
		})
	}

	return result, nil
}

func (c *SystemStatsCollector) collectProcesses(ctx context.Context, limit int) ([]*ProcessStat, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}

	type procInfo struct {
		pid        int32
		name       string
		cpuPercent float64
		memPercent float32
		memRSS     uint64
		username   string
		status     string
	}

	var procList []procInfo
	for _, p := range procs {
		name, err := p.NameWithContext(ctx)
		if err != nil {
			continue
		}

		cpuPct, _ := p.CPUPercentWithContext(ctx)    //nolint:errcheck // best-effort process stats
		memPct, _ := p.MemoryPercentWithContext(ctx) //nolint:errcheck // best-effort process stats

		var memRSS uint64
		if memInfo, err := p.MemoryInfoWithContext(ctx); err == nil && memInfo != nil {
			memRSS = memInfo.RSS
		}

		username, _ := p.UsernameWithContext(ctx)  //nolint:errcheck // best-effort process stats
		statusSlice, _ := p.StatusWithContext(ctx) //nolint:errcheck // best-effort process stats
		status := ""
		if len(statusSlice) > 0 {
			status = statusSlice[0]
		}

		procList = append(procList, procInfo{
			pid:        p.Pid,
			name:       name,
			cpuPercent: cpuPct,
			memPercent: memPct,
			memRSS:     memRSS,
			username:   username,
			status:     status,
		})
	}

	// Sort by CPU usage (descending)
	sort.Slice(procList, func(i, j int) bool {
		return procList[i].cpuPercent > procList[j].cpuPercent
	})

	// Limit results
	if limit > 0 && len(procList) > limit {
		procList = procList[:limit]
	}

	var result []*ProcessStat
	for _, p := range procList {
		result = append(result, &ProcessStat{
			Pid:           p.pid,
			Name:          p.name,
			CpuPercent:    p.cpuPercent,
			MemoryPercent: float64(p.memPercent),
			MemoryRss:     p.memRSS,
			Username:      p.username,
			Status:        p.status,
		})
	}

	return result, nil
}
