// Package gcmon pkg/gcmon/gcmon.go c1-util-debug
//
// A small sampler over the standard runtime/metrics package. It keeps a fixed
// ring of samples so garbage collector behavior can be read off a live
// process, and it behaves the same under Go and TinyGo because it only uses
// metric names. A metric the runtime does not know is reported as absent.
package gcmon

import (
	"math"
	"runtime"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"
)

// Interval is the time between samples.
const Interval = 5 * time.Second

// DefaultSamples is the default ring length, about 30 minutes at Interval.
const DefaultSamples = 360

// histEvery is how often the pause histogram is kept for windowed percentiles.
const histEvery = time.Minute

// histKeep is the number of kept histograms, enough to reach back 5 minutes.
const histKeep = 7

const (
	mLive = iota
	mGoal
	mLimit
	mCycles
	mPauses
	mCPUPause
	mCPUGC
	mCPUTotal
	mObjects
	mFree
	mReleased
	mMemTotal
	mGoroutines
	mCount
)

var metricNames = [mCount]string{
	mLive:       "/gc/heap/live:bytes",
	mGoal:       "/gc/heap/goal:bytes",
	mLimit:      "/gc/gomemlimit:bytes",
	mCycles:     "/gc/cycles/total:gc-cycles",
	mPauses:     "/gc/pauses:seconds",
	mCPUPause:   "/cpu/classes/gc/pause:cpu-seconds",
	mCPUGC:      "/cpu/classes/gc/total:cpu-seconds",
	mCPUTotal:   "/cpu/classes/total:cpu-seconds",
	mObjects:    "/memory/classes/heap/objects:bytes",
	mFree:       "/memory/classes/heap/free:bytes",
	mReleased:   "/memory/classes/heap/released:bytes",
	mMemTotal:   "/memory/classes/total:bytes",
	mGoroutines: "/sched/goroutines:goroutines",
}

// Sample is one reading. Have is a bitmask of the metrics that were present,
// bit i for the i-th name in metricNames, plus bitRSS and bitThreads.
type Sample struct {
	At         time.Time
	Have       uint32
	Live       uint64
	Goal       uint64
	Limit      uint64
	Cycles     uint64
	CPUPause   float64
	CPUGC      float64
	CPUTotal   float64
	Objects    uint64
	Free       uint64
	Released   uint64
	MemTotal   uint64
	Goroutines uint64
	RSS        uint64
	Threads    uint64
}

const (
	bitRSS     = 1 << 20
	bitThreads = 1 << 21
)

// Has reports whether metric i was present in the sample.
func (s *Sample) has(i int) bool { return s.Have&(1<<uint(i)) != 0 }

type hist struct {
	at     time.Time
	counts []uint64
}

// Sampler holds the ring. The zero value is not usable, use New.
type Sampler struct {
	mu      sync.Mutex
	ring    []Sample
	head    int // next slot to write
	n       int // samples held
	hists   [histKeep]hist
	hhead   int
	hn      int
	lastH   time.Time
	cur     []uint64
	buf     [mCount]metrics.Sample
	bounds  []float64
	procBuf []byte
	stop    chan struct{}
}

// New returns a sampler holding up to n samples. It does not sample by itself.
func New(n int) *Sampler {
	if n < 2 {
		n = 2
	}
	s := &Sampler{ring: make([]Sample, n), stop: make(chan struct{})}
	for i, name := range metricNames {
		s.buf[i].Name = name
	}
	return s
}

// Start samples now and then every Interval until Stop is called.
func (s *Sampler) Start() {
	s.Sample()
	go func() {
		t := time.NewTicker(Interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.Sample()
			case <-s.stop:
				return
			}
		}
	}()
}

// Stop ends the sampling goroutine.
func (s *Sampler) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

// Sample takes one reading and appends it to the ring.
func (s *Sampler) Sample() {
	s.mu.Lock()
	defer s.mu.Unlock()
	metrics.Read(s.buf[:])
	sm := &s.ring[s.head]
	*sm = Sample{At: time.Now()}
	u := func(i int, dst *uint64) {
		if v := s.buf[i].Value; v.Kind() == metrics.KindUint64 {
			*dst = v.Uint64()
			sm.Have |= 1 << uint(i)
		}
	}
	f := func(i int, dst *float64) {
		if v := s.buf[i].Value; v.Kind() == metrics.KindFloat64 {
			*dst = v.Float64()
			sm.Have |= 1 << uint(i)
		}
	}
	u(mLive, &sm.Live)
	u(mGoal, &sm.Goal)
	u(mLimit, &sm.Limit)
	u(mCycles, &sm.Cycles)
	f(mCPUPause, &sm.CPUPause)
	f(mCPUGC, &sm.CPUGC)
	f(mCPUTotal, &sm.CPUTotal)
	u(mObjects, &sm.Objects)
	u(mFree, &sm.Free)
	u(mReleased, &sm.Released)
	u(mMemTotal, &sm.MemTotal)
	u(mGoroutines, &sm.Goroutines)
	if rss, th := readProc(&s.procBuf); rss > 0 {
		sm.RSS, sm.Have = rss, sm.Have|bitRSS
		if th > 0 {
			sm.Threads, sm.Have = th, sm.Have|bitThreads
		}
	}
	if v := s.buf[mPauses].Value; v.Kind() == metrics.KindFloat64Histogram {
		h := v.Float64Histogram()
		if s.bounds == nil {
			s.bounds = append([]float64(nil), h.Buckets...)
		}
		s.cur = append(s.cur[:0], h.Counts...)
	}
	if v := s.buf[mPauses].Value; v.Kind() == metrics.KindFloat64Histogram && sm.At.Sub(s.lastH) >= histEvery-time.Second {
		h := v.Float64Histogram()
		slot := &s.hists[s.hhead]
		slot.at = sm.At
		slot.counts = append(slot.counts[:0], h.Counts...)
		s.hhead = (s.hhead + 1) % histKeep
		if s.hn < histKeep {
			s.hn++
		}
		s.lastH = sm.At
	}
	s.head = (s.head + 1) % len(s.ring)
	if s.n < len(s.ring) {
		s.n++
	}
}

// Samples returns a copy of the ring, oldest first.
func (s *Sampler) Samples() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.samplesLocked()
}

func (s *Sampler) samplesLocked() []Sample {
	out := make([]Sample, s.n)
	start := (s.head - s.n + len(s.ring)) % len(s.ring)
	for i := range out {
		out[i] = s.ring[(start+i)%len(s.ring)]
	}
	return out
}

var (
	defOnce    sync.Once
	def        *Sampler
	cfgSamples atomic.Int64
)

// Configure sets the ring length of the default sampler from the visor config.
// Zero keeps DefaultSamples. It has no effect once the sampler has started.
func Configure(samples int) { cfgSamples.Store(int64(samples)) }

// Default returns the process wide sampler, starting it on first use. Its ring
// length comes from Configure.
func Default() *Sampler {
	defOnce.Do(func() {
		n := DefaultSamples
		if c := int(cfgSamples.Load()); c > 1 {
			n = c
		}
		def = New(n)
		def.Start()
	})
	return def
}

// Quantile returns the q quantile of a bucketed histogram, as the upper bound
// of the bucket holding it. A final infinite bound is replaced by its lower bound.
func quantile(buckets []float64, counts []uint64, q float64) float64 {
	var total uint64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := uint64(math.Ceil(q * float64(total)))
	if target == 0 {
		target = 1
	}
	var run uint64
	for i, c := range counts {
		run += c
		if run >= target {
			return upper(buckets, i)
		}
	}
	return upper(buckets, len(counts)-1)
}

func upper(buckets []float64, i int) float64 {
	if i+1 < len(buckets) && !math.IsInf(buckets[i+1], 1) {
		return buckets[i+1]
	}
	return buckets[i]
}

func maxBucket(buckets []float64, counts []uint64) float64 {
	for i := len(counts) - 1; i >= 0; i-- {
		if counts[i] > 0 {
			return upper(buckets, i)
		}
	}
	return 0
}

// Compiler and GOMAXPROCS are read here so tests can rely on one place.
func compiler() string { return runtime.Compiler }
