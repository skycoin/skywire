package devices

import (
	psMem "github.com/shirou/gopsutil/v3/mem"
)

// Memory-usage components reported by UpdateMemBreakdown, mirroring multiload-ng's
// RAM graph: it splits the single "used %" into what the memory is actually
// doing — application-used vs. reclaimable buffers/cache — so a box that looks
// "full" is distinguishable from one that's merely caching.
const (
	MemUsed  = "used"  // application/anonymous memory (gopsutil Used)
	MemBuff  = "buff"  // I/O buffers
	MemCache = "cache" // page cache + reclaimable slab
	MemTotal = "total" // used + buff + cache (the real footprint)
)

// MemBreakdownLabels lists the keys UpdateMemBreakdown fills, ordered so a
// shade gradient runs used (darkest) -> total (lightest).
var MemBreakdownLabels = []string{MemUsed, MemBuff, MemCache, MemTotal}

// UpdateMemBreakdown fills m with each memory component as a percentage of total
// RAM. Buffers and cache are reclaimable, so they are shown distinctly from
// application memory rather than lumped into one "used" figure.
func UpdateMemBreakdown(m map[string]int) map[string]error {
	vm, err := psMem.VirtualMemory()
	if err != nil {
		return map[string]error{"Main": err}
	}
	if vm.Total == 0 {
		return nil
	}
	total := float64(vm.Total)
	cache := vm.Cached + vm.Sreclaimable
	pct := func(v uint64) int {
		p := float64(v) / total * 100.0
		switch {
		case p < 0:
			return 0
		case p > 100:
			return 100
		default:
			return int(p + 0.5)
		}
	}
	m[MemUsed] = pct(vm.Used)
	m[MemBuff] = pct(vm.Buffers)
	m[MemCache] = pct(cache)
	m[MemTotal] = pct(vm.Used + vm.Buffers + cache)
	return nil
}
