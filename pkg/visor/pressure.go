// Package visor pkg/visor/pressure.go c3-vis-core
//
// Resource pressure: whether the machine is short of memory, or the visor's
// CPU is going to its garbage collector. Under pressure the visor takes no new
// automatic transports. It refuses them at settlement and states it in its
// reach declaration, so autoconnect elsewhere stops picking it. A small board
// then settles at what it can carry instead of at a configured number.
package visor

import (
	"bufio"
	"context"
	"os"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

const (
	pressureInterval = 15 * time.Second
	// pressureWindow is how far back the GC share is measured.
	pressureWindow = time.Minute
	// Memory: pressure starts when the memory still available falls under the
	// smaller of a share of RAM and a fixed amount, and ends once it is back
	// over the smaller of a higher pair. The amount keeps a big host out of it.
	pressureMemOnShare  = 0.10
	pressureMemOnBytes  = 256 << 20
	pressureMemOffShare = 0.20
	pressureMemOffBytes = 512 << 20
	// GC: the share of the CPU available to the visor spent in the garbage
	// collector over pressureWindow.
	pressureGCOn  = 0.25
	pressureGCOff = 0.10
)

// pressureState is the visor's current resource pressure.
type pressureState struct {
	busy atomic.Bool

	mu sync.Mutex
	// memAvail and memTotal are the last memory reading, 0 when unknown.
	memAvail, memTotal int64
	// gcShare is the GC share of CPU over the last window.
	gcShare float64
	// since is when the current state began.
	since time.Time
}

// diag returns the pressure for visor state.
func (p *pressureState) diag() *visorapi.DiagPressure {
	p.mu.Lock()
	defer p.mu.Unlock()
	return &visorapi.DiagPressure{
		Busy:       p.busy.Load(),
		MemAvailMB: float64(p.memAvail) / (1 << 20),
		MemTotalMB: float64(p.memTotal) / (1 << 20),
		GCCPUShare: p.gcShare,
		Since:      p.since,
	}
}

// memTight reports whether avail of total is under pressure. on selects the
// threshold: the lower one to enter pressure, the higher one to stay in it.
func memTight(avail, total int64, on bool) bool {
	if avail <= 0 || total <= 0 {
		return false
	}
	share, bytes := pressureMemOffShare, int64(pressureMemOffBytes)
	if on {
		share, bytes = pressureMemOnShare, pressureMemOnBytes
	}
	return avail < min(int64(float64(total)*share), bytes)
}

// nextBusy decides the pressure state from one reading.
func nextBusy(busy bool, avail, total int64, gcShare float64) bool {
	if !busy {
		return memTight(avail, total, true) || gcShare > pressureGCOn
	}
	return memTight(avail, total, false) || gcShare > pressureGCOff
}

// gcSampler measures the share of CPU going to the garbage collector.
type gcSampler struct {
	samples []metrics.Sample
	hist    []gcPoint
}

type gcPoint struct {
	at        time.Time
	gc, total float64
}

func newGCSampler() *gcSampler {
	return &gcSampler{samples: []metrics.Sample{
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
		{Name: "/cpu/classes/total:cpu-seconds"},
	}}
}

// share reads the counters and returns the GC share since the oldest reading
// inside pressureWindow.
func (s *gcSampler) share(now time.Time) float64 {
	metrics.Read(s.samples)
	if s.samples[0].Value.Kind() != metrics.KindFloat64 || s.samples[1].Value.Kind() != metrics.KindFloat64 {
		return 0
	}
	p := gcPoint{at: now, gc: s.samples[0].Value.Float64(), total: s.samples[1].Value.Float64()}
	s.hist = append(s.hist, p)
	for len(s.hist) > 2 && now.Sub(s.hist[1].at) >= pressureWindow {
		s.hist = s.hist[1:]
	}
	first := s.hist[0]
	if d := p.total - first.total; d > 0 {
		return (p.gc - first.gc) / d
	}
	return 0
}

// availableMemory returns the memory still available to the visor and the
// total it may use, from the cgroup when it is limited and /proc/meminfo
// otherwise. Both are 0 where neither can be read.
func availableMemory() (avail, total int64) {
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "MemTotal:"):
				total = parseMemInfoLine(line)
			case strings.HasPrefix(line, "MemAvailable:"):
				avail = parseMemInfoLine(line)
			}
		}
	}
	limit := readCgroupInt("/sys/fs/cgroup/memory.max")
	used := readCgroupInt("/sys/fs/cgroup/memory.current")
	if limit > 0 && used > 0 && (total == 0 || limit < total) {
		total = limit
		if cg := limit - used; avail == 0 || cg < avail {
			avail = cg
		}
	}
	return avail, total
}

func readCgroupInt(path string) int64 {
	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0 // "max" or unreadable
	}
	return n
}

// runPressureLoop updates v.pressure until ctx ends.
func runPressureLoop(ctx context.Context, v *Visor, log *logging.Logger) {
	gc := newGCSampler()
	gc.share(time.Now())
	v.pressure.mu.Lock()
	v.pressure.since = time.Now()
	v.pressure.mu.Unlock()
	t := time.NewTicker(pressureInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			avail, total := availableMemory()
			share := gc.share(now)
			p := &v.pressure
			p.mu.Lock()
			p.memAvail, p.memTotal, p.gcShare = avail, total, share
			was := p.busy.Load()
			busy := nextBusy(was, avail, total, share)
			if busy != was {
				p.busy.Store(busy)
				p.since = now
			}
			p.mu.Unlock()
			if busy != was {
				l := log.WithField("mem_available_mib", avail>>20).
					WithField("mem_total_mib", total>>20).
					WithField("gc_cpu_share", strconv.FormatFloat(share, 'f', 2, 64))
				if busy {
					l.Warn("Under resource pressure: taking no new automatic transports")
				} else {
					l.Info("Resource pressure ended: taking automatic transports again")
				}
			}
		}
	}
}

// inboundGate is the transport manager's check on a new inbound transport.
// Under pressure it refuses automatic transports, except from this visor's
// hypervisors and from visors this visor is the hypervisor of. A metered
// mode refuses them the same way.
func (v *Visor) inboundGate(remote cipher.PubKey, label transport.Label) bool {
	if label != transport.LabelAutomatic || (!v.pressure.busy.Load() && !v.meter.lean.Load()) {
		return true
	}
	for _, hv := range v.conf.Hypervisors {
		if hv == remote {
			return true
		}
	}
	if hv := v.hvInstance; hv != nil {
		hv.mu.RLock()
		_, ok := hv.remoteVisors[remote]
		hv.mu.RUnlock()
		if ok {
			return true
		}
	}
	return false
}
