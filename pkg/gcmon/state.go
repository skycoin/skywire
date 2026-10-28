package gcmon

import (
	"math"
	"runtime"
	"time"
)

// Window is the derived figures over the most recent stretch of samples.
type Window struct {
	// CoveredS is the span actually covered, less than the window when the
	// sampler is younger than it.
	CoveredS float64 `json:"covered_s"`
	// PauseShare is the fraction of CPU time spent in stop-the-world pauses.
	PauseShare *float64 `json:"pause_share,omitempty"`
	// GCShare is the fraction of CPU time spent in the collector in total.
	GCShare           *float64 `json:"gc_share,omitempty"`
	CollectionsPerMin *float64 `json:"collections_per_min,omitempty"`
	PauseS            *float64 `json:"pause_seconds,omitempty"`
	Pauses            *Pauses  `json:"pauses,omitempty"`
}

// Pauses summarizes a pause histogram. Values are bucket upper bounds, so they
// round up to the histogram resolution.
type Pauses struct {
	Count uint64  `json:"count"`
	P50S  float64 `json:"p50_s"`
	P99S  float64 `json:"p99_s"`
	MaxS  float64 `json:"max_s"`
}

// State is the runtime section of the visor state snapshot. A pointer field is
// omitted when the runtime did not report that metric.
type State struct {
	Compiler   string `json:"compiler"`
	GoVersion  string `json:"go_version"`
	GOMAXPROCS int    `json:"gomaxprocs"`
	NumCPU     int    `json:"num_cpu"`

	Goroutines *uint64 `json:"goroutines,omitempty"`
	Threads    *uint64 `json:"threads,omitempty"`
	RSSBytes   *uint64 `json:"rss_bytes,omitempty"`

	HeapLiveBytes     *uint64  `json:"heap_live_bytes,omitempty"`
	HeapGoalBytes     *uint64  `json:"heap_goal_bytes,omitempty"`
	GoMemLimitBytes   *uint64  `json:"gomemlimit_bytes,omitempty"`
	HeapObjectsBytes  *uint64  `json:"heap_objects_bytes,omitempty"`
	HeapFreeBytes     *uint64  `json:"heap_free_bytes,omitempty"`
	HeapReleasedBytes *uint64  `json:"heap_released_bytes,omitempty"`
	MemoryTotalBytes  *uint64  `json:"memory_total_bytes,omitempty"`
	GCCycles          *uint64  `json:"gc_cycles,omitempty"`
	CPUTotalS         *float64 `json:"cpu_total_seconds,omitempty"`
	CPUGCS            *float64 `json:"cpu_gc_seconds,omitempty"`
	CPUGCPauseS       *float64 `json:"cpu_gc_pause_seconds,omitempty"`

	// LiveLimitRatio is live heap over the memory limit, omitted when no
	// limit is set.
	LiveLimitRatio *float64 `json:"live_limit_ratio,omitempty"`
	// PausesTotal covers the whole run.
	PausesTotal *Pauses `json:"pauses_total,omitempty"`

	Last1m *Window `json:"last_1m,omitempty"`
	Last5m *Window `json:"last_5m,omitempty"`

	SamplerIntervalS float64 `json:"sampler_interval_s"`
	SamplerSamples   int     `json:"sampler_samples"`
	SamplerCapacity  int     `json:"sampler_capacity"`
}

func ptrU(s *Sample, i int, v uint64) *uint64 {
	if !s.has(i) {
		return nil
	}
	return &v
}

func ptrF(s *Sample, i int, v float64) *float64 {
	if !s.has(i) {
		return nil
	}
	return &v
}

// State reads the latest sample and derives the window figures.
func (s *Sampler) State() *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &State{
		Compiler:         compiler(),
		GoVersion:        runtime.Version(),
		GOMAXPROCS:       runtime.GOMAXPROCS(0),
		NumCPU:           runtime.NumCPU(),
		SamplerIntervalS: Interval.Seconds(),
		SamplerSamples:   s.n,
		SamplerCapacity:  len(s.ring),
	}
	if s.n == 0 {
		return st
	}
	samples := s.samplesLocked()
	last := &samples[len(samples)-1]
	st.Goroutines = ptrU(last, mGoroutines, last.Goroutines)
	st.HeapLiveBytes = ptrU(last, mLive, last.Live)
	st.HeapGoalBytes = ptrU(last, mGoal, last.Goal)
	st.HeapObjectsBytes = ptrU(last, mObjects, last.Objects)
	st.HeapFreeBytes = ptrU(last, mFree, last.Free)
	st.HeapReleasedBytes = ptrU(last, mReleased, last.Released)
	st.MemoryTotalBytes = ptrU(last, mMemTotal, last.MemTotal)
	st.GCCycles = ptrU(last, mCycles, last.Cycles)
	st.CPUTotalS = ptrF(last, mCPUTotal, last.CPUTotal)
	st.CPUGCS = ptrF(last, mCPUGC, last.CPUGC)
	st.CPUGCPauseS = ptrF(last, mCPUPause, last.CPUPause)
	if last.Have&bitRSS != 0 {
		v := last.RSS
		st.RSSBytes = &v
	}
	if last.Have&bitThreads != 0 {
		v := last.Threads
		st.Threads = &v
	}
	// An unset limit reads as MaxInt64, or close to it.
	if last.has(mLimit) {
		v := last.Limit
		if v < 1<<62 {
			st.GoMemLimitBytes = &v
			if last.has(mLive) && v > 0 {
				r := float64(last.Live) / float64(v)
				st.LiveLimitRatio = &r
			}
		}
	}
	if len(s.cur) > 0 && len(s.cur)+1 == len(s.bounds) {
		st.PausesTotal = summarize(s.bounds, s.cur, nil)
	}
	st.Last1m = s.window(samples, time.Minute)
	st.Last5m = s.window(samples, 5*time.Minute)
	if st.Last5m != nil {
		st.Last5m.Pauses = s.windowPauses(last.At, 5*time.Minute)
	}
	return st
}

func summarize(bounds []float64, cur, base []uint64) *Pauses {
	d := make([]uint64, len(cur))
	var total uint64
	for i, c := range cur {
		if i < len(base) && base[i] <= c {
			c -= base[i]
		}
		d[i] = c
		total += c
	}
	p := &Pauses{Count: total}
	if total == 0 {
		return p
	}
	p.P50S = quantile(bounds, d, 0.5)
	p.P99S = quantile(bounds, d, 0.99)
	p.MaxS = maxBucket(bounds, d)
	return p
}

// windowPauses diffs the current histogram against the oldest kept one that is
// no older than the window plus some slack.
func (s *Sampler) windowPauses(now time.Time, w time.Duration) *Pauses {
	if len(s.cur) == 0 || len(s.cur)+1 != len(s.bounds) {
		return nil
	}
	oldest := (s.hhead - s.hn + histKeep) % histKeep
	for i := 0; i < s.hn; i++ {
		h := &s.hists[(oldest+i)%histKeep]
		if h.at.Before(now.Add(-w - histEvery/2)) {
			continue
		}
		return summarize(s.bounds, s.cur, h.counts)
	}
	return nil
}

// window derives the figures over the last w of samples, using the oldest
// sample that is within w (so a young sampler reports what it has).
func (s *Sampler) window(samples []Sample, w time.Duration) *Window {
	if len(samples) < 2 {
		return nil
	}
	last := &samples[len(samples)-1]
	base := &samples[0]
	for i := range samples {
		if !samples[i].At.Before(last.At.Add(-w - Interval/2)) {
			base = &samples[i]
			break
		}
	}
	wall := last.At.Sub(base.At).Seconds()
	if wall <= 0 {
		return nil
	}
	win := &Window{CoveredS: wall}
	cpu := wall * float64(runtime.GOMAXPROCS(0))
	if last.has(mCPUTotal) && base.has(mCPUTotal) && last.CPUTotal > base.CPUTotal {
		cpu = last.CPUTotal - base.CPUTotal
	}
	if last.has(mCPUPause) && base.has(mCPUPause) {
		d := math.Max(0, last.CPUPause-base.CPUPause)
		share := d / cpu
		win.PauseShare, win.PauseS = &share, &d
	}
	if last.has(mCPUGC) && base.has(mCPUGC) {
		share := math.Max(0, last.CPUGC-base.CPUGC) / cpu
		win.GCShare = &share
	}
	if last.has(mCycles) && base.has(mCycles) && last.Cycles >= base.Cycles {
		cpm := float64(last.Cycles-base.Cycles) / (wall / 60)
		win.CollectionsPerMin = &cpm
	}
	return win
}
