package rewardsserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerCatchUpAndInterval(t *testing.T) {
	var n atomic.Int32
	s := &Scheduler{Interval: 50 * time.Millisecond, Cycle: func(context.Context) error {
		n.Add(1)
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Start(ctx); close(done) }()

	// The catch-up cycle runs at once, not after the first interval.
	waitFor(t, func() bool { return n.Load() >= 1 }, 40*time.Millisecond)
	waitFor(t, func() bool { return n.Load() >= 3 }, time.Second)
	cancel()
	<-done

	st := s.Status()
	if st.Cycles < 3 || st.Failures != 0 || st.LastResult != "ok" || st.Running {
		t.Fatalf("status %+v", st)
	}
	if st.LastStart.IsZero() || st.LastEnd.Before(st.LastStart) || st.LastDuration == "" || st.NextRun.IsZero() {
		t.Fatalf("times %+v", st)
	}
	if st.Interval != "50ms" {
		t.Fatalf("interval %q", st.Interval)
	}
}

func TestSchedulerFailureContinues(t *testing.T) {
	var n atomic.Int32
	s := &Scheduler{Interval: 20 * time.Millisecond, Cycle: func(context.Context) error {
		switch n.Add(1) {
		case 1:
			return errors.New("bandwidth fetch failed")
		case 2:
			panic("calc blew up")
		}
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	waitFor(t, func() bool { return s.Status().Cycles >= 1 }, time.Second)
	if st := s.Status(); st.LastResult != "failed" || st.LastError != "bandwidth fetch failed" {
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
	s := &Scheduler{Interval: time.Millisecond, Cycle: func(context.Context) error {
		r := running.Add(1)
		for {
			m := maxRunning.Load()
			if r <= m || maxRunning.CompareAndSwap(m, r) {
				break
			}
		}
		<-release
		running.Add(-1)
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)
	// Manual runs while the scheduled cycle is held must wait their turn.
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() { _ = s.RunOnce(ctx) }) //nolint:errcheck
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
	s := &Scheduler{Cycle: func(context.Context) error { return errors.New("no uptime data") }}
	SetScheduler(s)
	defer SetScheduler(nil)
	if got := cycleActive(); got != "inactive" {
		t.Fatalf("cycleActive %q", got)
	}
	_ = s.RunOnce(context.Background()) //nolint:errcheck

	w := httptest.NewRecorder()
	buildRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body struct {
		Cycle *CycleStatus `json:"reward_cycle"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Cycle == nil {
		t.Fatalf("health %s: %v", w.Body.String(), err)
	}
	if body.Cycle.Cycles != 1 || body.Cycle.LastResult != "failed" || body.Cycle.LastError != "no uptime data" || body.Cycle.LastDuration == "" {
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
