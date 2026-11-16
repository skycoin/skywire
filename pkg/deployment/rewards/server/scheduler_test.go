package rewardsserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// calcFunc is a Calc that counts its runs and keeps their reasons.
type calcFunc struct {
	mu      sync.Mutex
	reasons []string
	res     CalcResult
	err     error
}

func (c *calcFunc) calc(_ context.Context, reason string) (CalcResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reasons = append(c.reasons, reason)
	return c.res, c.err
}

func (c *calcFunc) runs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.reasons...)
}

func startScheduler(t *testing.T, s *Scheduler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Start(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestSchedulerStartAndFallback(t *testing.T) {
	c := &calcFunc{}
	s := &Scheduler{Interval: 50 * time.Millisecond, Cycle: Cycle{Calc: c.calc}}
	startScheduler(t, s)

	// The start run is at once, not after the first interval.
	waitFor(t, func() bool { return len(c.runs()) >= 1 }, 40*time.Millisecond)
	waitFor(t, func() bool { return len(c.runs()) >= 3 }, time.Second)
	if r := c.runs(); r[0] != "start" || r[1] != "hourly check" {
		t.Fatalf("reasons %q", r)
	}
	st := s.Status()
	if st.Cycles < 3 || st.Failures != 0 || st.LastResult != "ok" || st.Running {
		t.Fatalf("status %+v", st)
	}
	if st.LastStart.IsZero() || st.LastEnd.Before(st.LastStart) || st.LastDuration == "" || st.NextRun.IsZero() {
		t.Fatalf("times %+v", st)
	}
	if st.Interval != "50ms" || st.Debounce != "1m0s" || st.CollectInterval != "" {
		t.Fatalf("intervals %+v", st)
	}
}

// A burst of triggers gives one Calc, one debounce after the first.
func TestSchedulerDebounceCoalesces(t *testing.T) {
	c := &calcFunc{}
	s := &Scheduler{Interval: time.Hour, Debounce: 100 * time.Millisecond, Cycle: Cycle{Calc: c.calc}}
	startScheduler(t, s)
	waitFor(t, func() bool { return len(c.runs()) == 1 }, time.Second)

	for _, pk := range []string{"pk1", "pk2", "pk1", "pk3"} {
		s.Trigger("survey " + pk)
		time.Sleep(10 * time.Millisecond)
	}
	if p := s.Status().Pending; p != "survey pk1 and 2 more" {
		t.Fatalf("pending %q", p)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(c.runs()); n != 1 {
		t.Fatalf("%d runs before the debounce ended", n)
	}
	waitFor(t, func() bool { return len(c.runs()) == 2 }, time.Second)
	time.Sleep(200 * time.Millisecond)
	r := c.runs()
	if len(r) != 2 || r[1] != "survey pk1 and 2 more" {
		t.Fatalf("runs %q", r)
	}
	if st := s.Status(); st.LastTrigger != r[1] || st.Pending != "" {
		t.Fatalf("status %+v", st)
	}
}

// Only a survey change the calculation can see triggers a Calc.
func TestSchedulerSurveyStored(t *testing.T) {
	c := &calcFunc{}
	relevant := func(old, new []byte) bool { return !strings.Contains(string(new), "timestamp only") }
	s := &Scheduler{Interval: time.Hour, Debounce: 20 * time.Millisecond, Cycle: Cycle{Calc: c.calc, SurveyChanged: relevant}}
	startScheduler(t, s)
	waitFor(t, func() bool { return len(c.runs()) == 1 }, time.Second)

	s.SurveyStored("pk1", []byte("a"), []byte("timestamp only"))
	time.Sleep(100 * time.Millisecond)
	if n := len(c.runs()); n != 1 {
		t.Fatalf("%d runs after an irrelevant change", n)
	}
	s.SurveyStored("pk1", []byte("a"), []byte("new reward address"))
	waitFor(t, func() bool { return len(c.runs()) == 2 }, time.Second)
	if r := c.runs(); r[1] != "survey pk1" {
		t.Fatalf("runs %q", r)
	}

	// Without SurveyChanged, any different bytes count.
	s2 := &Scheduler{Cycle: Cycle{Calc: c.calc}}
	s2.SurveyStored("pk2", []byte("a"), []byte("a"))
	if s2.Status().Pending != "" {
		t.Fatal("identical survey triggered")
	}
	s2.SurveyStored("pk2", []byte("a"), []byte("b"))
	if s2.Status().Pending != "survey pk2" {
		t.Fatal("changed survey did not trigger")
	}
}

func TestSchedulerSkipsAndHash(t *testing.T) {
	c := &calcFunc{res: CalcResult{Date: "2026-11-15", InputsHash: "abc", Skipped: true, Note: "inputs unchanged"}}
	s := &Scheduler{Cycle: Cycle{Calc: c.calc}}
	_ = s.RunOnce(context.Background(), "hourly check") //nolint:errcheck
	st := s.Status()
	if st.Skips != 1 || st.Recalcs != 0 || st.LastResult != "skipped" || st.LastSkip != "inputs unchanged" ||
		st.InputsHash != "abc" || st.LastDate != "2026-11-15" || st.LastTrigger != "hourly check" || st.LastSkipAt.IsZero() {
		t.Fatalf("status %+v", st)
	}
	c.res.Skipped = false
	_ = s.RunOnce(context.Background(), "survey pk") //nolint:errcheck
	if st := s.Status(); st.Recalcs != 1 || st.LastResult != "ok" || st.LastRecalcAt.IsZero() {
		t.Fatalf("status %+v", st)
	}
}

// A spend the watch sees recalculates at once. A watch that sees none does not.
func TestSchedulerWatch(t *testing.T) {
	c := &calcFunc{}
	var spent atomic.Bool
	watch := func(context.Context) (bool, error) { return spent.Swap(false), nil }
	s := &Scheduler{Interval: time.Hour, WatchInterval: 10 * time.Millisecond, Cycle: Cycle{Calc: c.calc, Watch: watch}}
	startScheduler(t, s)
	waitFor(t, func() bool { return len(c.runs()) == 1 }, time.Second)
	time.Sleep(60 * time.Millisecond)
	if n := len(c.runs()); n != 1 {
		t.Fatalf("%d runs with no spend", n)
	}
	spent.Store(true)
	waitFor(t, func() bool { return len(c.runs()) == 2 }, time.Second)
	if r := c.runs(); r[1] != "payout address sent coins" {
		t.Fatalf("runs %q", r)
	}
	if s.Status().LastWatch.IsZero() {
		t.Fatal("no last_watch")
	}
}

func TestSchedulerCollect(t *testing.T) {
	c := &calcFunc{}
	var collects atomic.Int32
	collect := func(context.Context) error {
		if collects.Add(1) == 1 {
			return errors.New("tpd down")
		}
		return nil
	}
	s := &Scheduler{Interval: time.Hour, CollectInterval: 30 * time.Millisecond, Cycle: Cycle{Calc: c.calc, Collect: collect}}
	startScheduler(t, s)
	waitFor(t, func() bool { return len(c.runs()) == 1 }, time.Second)
	if st := s.Status(); collects.Load() != 1 || st.CollectError != "tpd down" || st.NextCollect.IsZero() {
		t.Fatalf("after the start collection: %d, %+v", collects.Load(), st)
	}
	waitFor(t, func() bool { return len(c.runs()) == 2 }, time.Second)
	if r := c.runs(); r[1] != "collection" || s.Status().CollectError != "" {
		t.Fatalf("runs %q, %+v", r, s.Status())
	}
}

func TestSchedulerUntilDayClose(t *testing.T) {
	s := &Scheduler{}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 16, h, m, 0, 0, time.UTC) }
	if d := s.untilDayClose(at(0, 5)); d != 10*time.Minute {
		t.Fatalf("at 00:05 %v", d)
	}
	if d := s.untilDayClose(at(0, 15)); d != 24*time.Hour {
		t.Fatalf("at 00:15 %v", d)
	}
	if d := s.untilDayClose(at(23, 0)); d != 75*time.Minute {
		t.Fatalf("at 23:00 %v", d)
	}
}

func TestSchedulerFailureContinues(t *testing.T) {
	var n atomic.Int32
	calc := func(context.Context, string) (CalcResult, error) {
		switch n.Add(1) {
		case 1:
			return CalcResult{}, errors.New("uptime fetch failed")
		case 2:
			panic("calc blew up")
		}
		return CalcResult{}, nil
	}
	s := &Scheduler{Interval: 20 * time.Millisecond, Cycle: Cycle{Calc: calc}}
	startScheduler(t, s)

	waitFor(t, func() bool { return s.Status().Cycles >= 1 }, time.Second)
	if st := s.Status(); st.LastResult != "failed" || st.LastError != "uptime fetch failed" {
		t.Fatalf("after error %+v", st)
	}
	waitFor(t, func() bool { return s.Status().Cycles >= 3 }, time.Second)
	st := s.Status()
	if st.Failures != 2 || st.LastResult != "ok" || st.LastError != "" {
		t.Fatalf("after recovery %+v", st)
	}
}

func TestSchedulerNoOverlap(t *testing.T) {
	var running, maxRunning atomic.Int32
	release := make(chan struct{})
	calc := func(context.Context, string) (CalcResult, error) {
		r := running.Add(1)
		for {
			m := maxRunning.Load()
			if r <= m || maxRunning.CompareAndSwap(m, r) {
				break
			}
		}
		<-release
		running.Add(-1)
		return CalcResult{}, nil
	}
	s := &Scheduler{Interval: time.Millisecond, Cycle: Cycle{Calc: calc}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)
	// Manual runs while the scheduled cycle is held must wait their turn.
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() { _ = s.RunOnce(ctx, "manual") }) //nolint:errcheck
	}
	waitFor(t, func() bool { return s.Status().Running }, time.Second)
	time.Sleep(20 * time.Millisecond)
	for range 6 {
		release <- struct{}{}
	}
	wg.Wait()
	if m := maxRunning.Load(); m != 1 {
		t.Fatalf("%d cycles ran at once", m)
	}
}

func TestHealthReportsScheduler(t *testing.T) {
	wd = t.TempDir()
	disableTpVizAPI, healthOnly = true, true
	t.Cleanup(func() { healthOnly = false })
	s := &Scheduler{Cycle: Cycle{Calc: func(context.Context, string) (CalcResult, error) {
		return CalcResult{}, errors.New("no uptime data")
	}}}
	SetScheduler(s)
	defer SetScheduler(nil)
	if got := cycleActive(); got != "inactive" {
		t.Fatalf("cycleActive %q", got)
	}
	_ = s.RunOnce(context.Background(), "start") //nolint:errcheck

	w := httptest.NewRecorder()
	buildRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body struct {
		Cycle *CycleStatus `json:"reward_cycle"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Cycle == nil {
		t.Fatalf("health %s: %v", w.Body.String(), err)
	}
	if body.Cycle.Cycles != 1 || body.Cycle.LastResult != "failed" || body.Cycle.LastError != "no uptime data" ||
		body.Cycle.LastDuration == "" || body.Cycle.LastTrigger != "start" {
		t.Fatalf("reward_cycle %+v", *body.Cycle)
	}
	if txt := cycleStatusText(); txt == "" {
		t.Fatal("no status text with a scheduler")
	}
}

func waitFor(t *testing.T, cond func() bool, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %v", d)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
