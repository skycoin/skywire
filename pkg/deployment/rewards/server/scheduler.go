// Package rewardsserver pkg/deployment/rewards/server/scheduler.go c3-app-rewards
//
// The hourly reward cycle inside the server process, in place of the
// systemd timer and oneshot unit. One cycle runs at start, to catch up,
// then one each interval. Cycles never overlap, and a failed one is logged
// and tried again at the next interval.
package rewardsserver

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"

	"github.com/skycoin/skywire/pkg/logging"
)

// DefaultCycleInterval is how often the scheduler runs a reward cycle.
const DefaultCycleInterval = time.Hour

// Scheduler runs a reward cycle at start and then every Interval.
type Scheduler struct {
	// Cycle is one reward cycle. It may block for minutes.
	Cycle func(ctx context.Context) error
	// Interval is the time from one cycle start to the next, DefaultCycleInterval when zero.
	Interval time.Duration
	Log      *logging.Logger

	// run is held for the whole of a cycle, so two never overlap.
	run sync.Mutex
	mu  sync.Mutex
	st  CycleStatus
}

// CycleStatus is the scheduler's state, as /health shows it.
type CycleStatus struct {
	Interval     string    `json:"interval"`
	Running      bool      `json:"running"`
	Cycles       int       `json:"cycles"`
	Failures     int       `json:"failures"`
	LastStart    time.Time `json:"last_start,omitzero"`
	LastEnd      time.Time `json:"last_end,omitzero"`
	LastDuration string    `json:"last_duration,omitempty"`
	LastResult   string    `json:"last_result,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	NextRun      time.Time `json:"next_run,omitzero"`
}

func (s *Scheduler) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return DefaultCycleInterval
}

// Start runs a cycle now and then every interval until ctx ends. A cycle
// still going when ctx ends is waited for.
func (s *Scheduler) Start(ctx context.Context) {
	for {
		start := time.Now()
		_ = s.RunOnce(ctx) //nolint:errcheck // RunOnce logs and records it
		next := start.Add(s.interval())
		s.mu.Lock()
		s.st.NextRun = next.UTC()
		s.mu.Unlock()
		wait := time.Until(next)
		if wait < 0 {
			wait = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// RunOnce runs one cycle, after any cycle in progress has finished.
func (s *Scheduler) RunOnce(ctx context.Context) (err error) {
	s.run.Lock()
	defer s.run.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	start := time.Now()
	s.mu.Lock()
	s.st.Running, s.st.LastStart = true, start.UTC()
	s.mu.Unlock()
	s.logger().Info("Reward cycle starting")

	err = s.call(ctx)

	end := time.Now()
	s.mu.Lock()
	s.st.Running = false
	s.st.Cycles++
	s.st.LastEnd = end.UTC()
	s.st.LastDuration = end.Sub(start).Round(time.Millisecond).String()
	if err != nil {
		s.st.Failures++
		s.st.LastResult, s.st.LastError = "failed", err.Error()
	} else {
		s.st.LastResult, s.st.LastError = "ok", ""
	}
	s.mu.Unlock()
	if err != nil {
		s.logger().WithError(err).WithField("retry_in", s.interval()).Error("Reward cycle failed")
	} else {
		s.logger().WithField("duration", end.Sub(start).Round(time.Millisecond)).Info("Reward cycle done")
	}
	return err
}

// call runs Cycle, turning a panic into an error so the server lives on.
func (s *Scheduler) call(ctx context.Context) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n\n%s", p, debug.Stack())
		}
	}()
	if s.Cycle == nil {
		return fmt.Errorf("no reward cycle configured")
	}
	return s.Cycle(ctx)
}

// Status returns a copy of the scheduler's state.
func (s *Scheduler) Status() CycleStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	st.Interval = s.interval().String()
	return st
}

func (s *Scheduler) logger() *logging.Logger {
	if s.Log != nil {
		return s.Log
	}
	return log
}

var (
	schedMu   sync.RWMutex
	scheduler *Scheduler
)

// SetScheduler makes /health report s. Nil reports the systemd units instead.
func SetScheduler(s *Scheduler) {
	schedMu.Lock()
	scheduler = s
	schedMu.Unlock()
}

func currentScheduler() *Scheduler {
	schedMu.RLock()
	defer schedMu.RUnlock()
	return scheduler
}

// cycleActive is "active" while a reward cycle runs, as systemctl is-active
// says it of the oneshot unit when no scheduler runs in this process.
func cycleActive() string {
	if s := currentScheduler(); s != nil {
		if s.Status().Running {
			return "active"
		}
		return "inactive"
	}
	active, _ := script.Exec(`systemctl is-active skywire-reward.service`).String() //nolint:errcheck,gosec
	return strings.TrimRight(active, "\n")
}

// cycleStatusText is the cycle status the log collection page shows, or ""
// when the cycle runs from the systemd timer.
func cycleStatusText() string {
	s := currentScheduler()
	if s == nil {
		return ""
	}
	st := s.Status()
	if st.Running {
		return "Reward cycle running since:\n" + st.LastStart.UTC().Format(time.RFC3339)
	}
	txt := "Next reward cycle:\n" + st.NextRun.UTC().Format(time.RFC3339)
	if st.LastResult != "" {
		txt += fmt.Sprintf("\nPrevious run: %s, %s", st.LastResult, st.LastDuration)
	}
	return txt
}

// CycleConfig is a reward cycle for a host without a command line. The
// calculation takes the defaults of `rewards run`.
type CycleConfig struct {
	// WorkDir holds hist/, log_backups/ and xpub_index.json.
	WorkDir     string
	SkycoinNode string
	SkipCollect bool
	// Fetch collects surveys from the visors, through Proxy when set,
	// instead of only cleaning the surveys they push.
	Fetch      bool
	Proxy      string
	MinVersion string
	// PruneBelow is --prune-below-version, "auto" when empty.
	PruneBelow string
}

// NewCycle returns the reward cycle for c. The skywire cli rewards package
// sets it, so a binary without that package has no cycle.
var NewCycle func(c CycleConfig) func(context.Context) error
