package gcmon

import (
	"encoding/json"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLiveSampleHasCoreMetrics(t *testing.T) {
	runtime.GC()
	s := New(4)
	s.Sample()
	st := s.State()
	if st.HeapLiveBytes == nil || *st.HeapLiveBytes == 0 {
		t.Fatalf("heap live missing: %+v", st)
	}
	if st.GCCycles == nil || st.Goroutines == nil || st.Compiler == "" {
		t.Fatalf("core fields missing: %+v", st)
	}
	if runtime.GOOS == "linux" && (st.RSSBytes == nil || st.Threads == nil) {
		t.Fatalf("rss or threads missing on linux")
	}
	if st.Last1m != nil {
		t.Fatalf("one sample cannot make a window")
	}
}

func TestRingWraps(t *testing.T) {
	s := New(3)
	for i := 0; i < 5; i++ {
		s.Sample()
	}
	if got := len(s.Samples()); got != 3 {
		t.Fatalf("samples = %d, want 3", got)
	}
}

// feed puts synthetic samples into the ring, one per Interval.
func feed(s *Sampler, start time.Time, f func(i uint64) Sample, n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := uint64(0); i < n; i++ {
		sm := f(i)
		sm.At = start.Add(time.Duration(i) * Interval)
		s.ring[s.head] = sm
		s.head = (s.head + 1) % len(s.ring)
		if s.n < len(s.ring) {
			s.n++
		}
	}
}

func TestWindowDerivedFigures(t *testing.T) {
	s := New(100)
	procs := float64(runtime.GOMAXPROCS(0))
	all := uint32(1<<mCycles | 1<<mCPUPause | 1<<mCPUGC | 1<<mCPUTotal | 1<<mLive | 1<<mLimit)
	// 60 samples over 295 s; each step is 5 s wall, 2 cycles, 0.01 s pause.
	feed(s, time.Now().Add(-295*time.Second), func(i uint64) Sample {
		return Sample{
			Have: all, Cycles: 2 * i, CPUPause: 0.01 * float64(i),
			CPUGC: 0.05 * float64(i), CPUTotal: 5 * procs * float64(i),
			Live: 400, Limit: 1000,
		}
	}, 60)
	st := s.State()
	if st.LiveLimitRatio == nil || *st.LiveLimitRatio != 0.4 {
		t.Fatalf("live/limit = %v", st.LiveLimitRatio)
	}
	w := st.Last1m
	if w == nil || w.CollectionsPerMin == nil || w.PauseShare == nil || w.GCShare == nil {
		t.Fatalf("1m window = %+v", w)
	}
	if got := *w.CollectionsPerMin; got < 23.9 || got > 24.1 {
		t.Fatalf("collections/min = %v, want 24", got)
	}
	if got := *w.PauseShare; got < 0.0019/procs || got > 0.0021/procs {
		t.Fatalf("pause share = %v, want 0.002/procs", got)
	}
	if got := *w.GCShare; got < 0.0099/procs || got > 0.0101/procs {
		t.Fatalf("gc share = %v, want 0.01/procs", got)
	}
	if st.Last5m == nil || st.Last5m.CoveredS < 290 {
		t.Fatalf("5m window = %+v", st.Last5m)
	}
}

func TestUnsetLimitIsOmitted(t *testing.T) {
	s := New(4)
	feed(s, time.Now(), func(uint64) Sample {
		return Sample{Have: 1<<mLive | 1<<mLimit, Live: 10, Limit: 1<<63 - 1}
	}, 2)
	st := s.State()
	if st.GoMemLimitBytes != nil || st.LiveLimitRatio != nil {
		t.Fatalf("unset limit leaked: %+v", st)
	}
}

func TestAbsentMetricsAreOmitted(t *testing.T) {
	s := New(4)
	feed(s, time.Now(), func(uint64) Sample { return Sample{} }, 2)
	b, _ := json.Marshal(s.State()) //nolint:errcheck
	for _, k := range []string{"heap_live_bytes", "gc_cycles", "rss_bytes", "pause_share"} {
		if strings.Contains(string(b), k) {
			t.Fatalf("%s present for an absent metric: %s", k, b)
		}
	}
}

func TestQuantiles(t *testing.T) {
	buckets := []float64{0, 1, 2, 4, 8}
	counts := []uint64{0, 90, 9, 1}
	if got := quantile(buckets, counts, 0.5); got != 2 {
		t.Fatalf("p50 = %v", got)
	}
	if got := quantile(buckets, counts, 0.99); got != 4 {
		t.Fatalf("p99 = %v", got)
	}
	if got := maxBucket(buckets, counts); got != 8 {
		t.Fatalf("max = %v", got)
	}
	p := summarize(buckets, counts, []uint64{0, 90, 0, 0})
	if p.Count != 10 || p.P50S != 4 {
		t.Fatalf("windowed = %+v", p)
	}
}

func TestHandlerTextAndJSON(t *testing.T) {
	s := New(8)
	feed(s, time.Now(), func(i uint64) Sample {
		return Sample{Have: 1<<mCycles | 1<<mCPUPause, Cycles: 3 * i, CPUPause: 0.5 * float64(i), Live: 7, Goal: 9}
	}, 3)
	h := Handler(func() *Sampler { return s })

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/debug/gctrace", nil))
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "#") {
		t.Fatalf("text body:\n%s", rec.Body.String())
	}
	f := strings.Fields(lines[3])
	if len(f) != 8 || f[1] != "3" || f[2] != "0.500000" || f[3] != "7" || f[4] != "9" {
		t.Fatalf("last line = %v", f)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/debug/gctrace?json=1", nil))
	var got []TraceLine
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 3 || got[2].CyclesDelta != 3 {
		t.Fatalf("json = %v err=%v", got, err)
	}
}

func TestSampleDoesNotGrowHeap(t *testing.T) {
	s := New(8)
	for i := 0; i < 20; i++ {
		s.Sample()
	}
	allocs := testing.AllocsPerRun(50, s.Sample)
	t.Logf("allocs per Sample: %v", allocs)
	if allocs > 12 {
		t.Fatalf("allocs per sample = %v", allocs)
	}
}

func BenchmarkSample(b *testing.B) {
	s := New(360)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Sample()
	}
}
