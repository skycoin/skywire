package devices

import (
	"sync"

	psCpu "github.com/shirou/gopsutil/v3/cpu"
)

// CPU-time states reported by UpdateCPUBreakdown, in a fixed order. Unlike the
// single "busy" figure reported by UpdateCPU (gopsutil cpu.Percent, which is
// just 100-idle), these mirror multiload-ng's CPU graph: they expose *what* the
// CPU is busy doing, including iowait (idle while blocked on I/O), which the
// aggregate busy number hides entirely.
const (
	CPUStateUser  = "usr"   // user-mode (includes guest, as the kernel counts it)
	CPUStateNice  = "nice"  // low-priority (niced) user-mode
	CPUStateSys   = "sys"   // kernel-mode
	CPUStateIO    = "io"    // iowait: idle while waiting on I/O
	CPUStateIRQ   = "irq"   // hardware + soft interrupt servicing
	CPUStateSteal = "steal" // time stolen by the hypervisor (VMs)
	CPUStateTotal = "total" // all non-idle time (sum of the above)
)

// CPUBreakdownLabels is the set of keys UpdateCPUBreakdown fills, ordered for
// stable iteration by callers. (The line-graph assigns colors by sorted key
// independently of this order.)
var CPUBreakdownLabels = []string{
	CPUStateUser,
	CPUStateNice,
	CPUStateSys,
	CPUStateIO,
	CPUStateIRQ,
	CPUStateSteal,
	CPUStateTotal,
}

var (
	cpuBreakdownMu   sync.Mutex
	lastCPUTimes     psCpu.TimesStat
	haveLastCPUTimes bool
)

// UpdateCPUBreakdown fills cpus with the percentage of total CPU time spent in
// each kernel CPU-time state since the previous call, aggregated across all
// logical CPUs. The first call has no prior snapshot to diff against and
// returns no data (the map is left untouched).
func UpdateCPUBreakdown(cpus map[string]int) map[string]error {
	ts, err := psCpu.Times(false)
	if err != nil {
		return map[string]error{"gopsutil": err}
	}
	if len(ts) == 0 {
		return nil
	}
	cur := ts[0]

	cpuBreakdownMu.Lock()
	defer cpuBreakdownMu.Unlock()
	prev := lastCPUTimes
	lastCPUTimes = cur
	if !haveLastCPUTimes {
		haveLastCPUTimes = true
		return nil
	}

	// On Linux /proc/stat, the user field already includes guest time and nice
	// includes guest_nice, so we use them as-is to avoid double counting.
	dUser := cur.User - prev.User
	dNice := cur.Nice - prev.Nice
	dSys := cur.System - prev.System
	dIO := cur.Iowait - prev.Iowait
	dIRQ := (cur.Irq + cur.Softirq) - (prev.Irq + prev.Softirq)
	dSteal := cur.Steal - prev.Steal
	dIdle := cur.Idle - prev.Idle

	busy := dUser + dNice + dSys + dIO + dIRQ + dSteal
	total := busy + dIdle
	if total <= 0 {
		return nil
	}

	pct := func(d float64) int {
		v := d / total * 100.0
		switch {
		case v < 0:
			return 0
		case v > 100:
			return 100
		default:
			return int(v + 0.5)
		}
	}
	cpus[CPUStateUser] = pct(dUser)
	cpus[CPUStateNice] = pct(dNice)
	cpus[CPUStateSys] = pct(dSys)
	cpus[CPUStateIO] = pct(dIO)
	cpus[CPUStateIRQ] = pct(dIRQ)
	cpus[CPUStateSteal] = pct(dSteal)
	cpus[CPUStateTotal] = pct(busy)
	return nil
}
