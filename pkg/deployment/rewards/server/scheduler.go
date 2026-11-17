// Package rewardsserver pkg/deployment/rewards/server/scheduler.go c3-app-rewards
//
// The reward cycle inside the server process, in place of the systemd timer
// and oneshot unit. The unpaid day is calculated at start, then again when
// one of its inputs may have changed: a pushed survey that differs where the
// calculation reads it, a payout address that sent coins, a collection or
// the close of a day. An hourly check is the safety net. Each calculation
// hashes its inputs first and writes nothing when they did not change.
// Work never overlaps, and a failed run is logged and tried again.
package rewardsserver

import (
	"bytes"
	"context"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"

	"github.com/skycoin/skywire/pkg/logging"
)

// Defaults of the Scheduler intervals.
const (
	DefaultCycleInterval   = time.Hour
	DefaultCollectInterval = 3 * time.Hour
	DefaultDebounce        = time.Minute
	DefaultWatchInterval   = 30 * time.Second
	DefaultDayCloseDelay   = 15 * time.Minute
)

// maxPending caps the trigger reasons kept for one debounced run.
const maxPending = 64

// Cycle is the reward work the Scheduler runs. Only Calc is required.
type Cycle struct {
	// Calc calculates the unpaid day, unless it was paid or its inputs did
	// not change. reason says what asked for it. It may block for minutes.
	Calc func(ctx context.Context, reason string) (CalcResult, error)
	// Collect cleans the surveys and collects the current day's bandwidth.
	Collect func(ctx context.Context) error
	// Watch reports whether a payout address of the unpaid day has sent
	// coins since it last looked.
	Watch func(ctx context.Context) (bool, error)
	// SurveyChanged reports whether a survey replacing old can change the
	// calculation. Nil compares the bytes.
	SurveyChanged func(old, new []byte) bool
}

// CalcResult is what one Calc did.
type CalcResult struct {
	Date       string
	InputsHash string
	Skipped    bool
	// Note says why it was skipped.
	Note string
}

// Scheduler runs a Cycle: Calc at start, on Trigger after Debounce, after
// each Collect, at DayCloseDelay past midnight UTC, when Watch saw a spend,
// and every Interval.
type Scheduler struct {
	Cycle Cycle
	// Interval is the fallback Calc, DefaultCycleInterval when zero.
	Interval time.Duration
	// CollectInterval spaces the collections, DefaultCollectInterval when zero.
	CollectInterval time.Duration
	// Debounce is how long a Trigger waits, DefaultDebounce when zero. The
	// triggers that come in meanwhile join the same Calc.
	Debounce time.Duration
	// WatchInterval spaces the Watch calls, DefaultWatchInterval when zero.
	WatchInterval time.Duration
	// DayCloseDelay is when, after midnight UTC, the finished day is final.
	DayCloseDelay time.Duration
	Log           *logging.Logger

	// run is held for the whole of a Calc, a Collect or a payout, so two never overlap.
	run      sync.Mutex
	mu       sync.Mutex
	st       CycleStatus
	pending  []string
	dropped  int
	kick     chan struct{}
	kickOnce sync.Once
}

// CycleStatus is the scheduler's state, as /health shows it.
type CycleStatus struct {
	Interval        string `json:"interval"`
	CollectInterval string `json:"collect_interval,omitempty"`
	Debounce        string `json:"debounce"`
	Running         bool   `json:"running"`
	// Cycles counts the calculation runs, Recalcs those that wrote the
	// outputs and Skips those that found the day paid or its inputs unchanged.
	Cycles       int       `json:"cycles"`
	Recalcs      int       `json:"recalcs"`
	Skips        int       `json:"skips"`
	Failures     int       `json:"failures"`
	LastStart    time.Time `json:"last_start,omitzero"`
	LastEnd      time.Time `json:"last_end,omitzero"`
	LastDuration string    `json:"last_duration,omitempty"`
	LastResult   string    `json:"last_result,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	LastTrigger  string    `json:"last_trigger,omitempty"`
	LastDate     string    `json:"last_date,omitempty"`
	InputsHash   string    `json:"inputs_hash,omitempty"`
	LastSkip     string    `json:"last_skip,omitempty"`
	LastSkipAt   time.Time `json:"last_skip_at,omitzero"`
	LastRecalcAt time.Time `json:"last_recalc_at,omitzero"`
	Pending      string    `json:"pending_trigger,omitempty"`
	NextRun      time.Time `json:"next_run,omitzero"`
	LastCollect  time.Time `json:"last_collect,omitzero"`
	CollectError string    `json:"collect_error,omitempty"`
	NextCollect  time.Time `json:"next_collect,omitzero"`
	LastWatch    time.Time `json:"last_watch,omitzero"`
	WatchError   string    `json:"watch_error,omitempty"`
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func (s *Scheduler) interval() time.Duration { return orDefault(s.Interval, DefaultCycleInterval) }
func (s *Scheduler) debounce() time.Duration { return orDefault(s.Debounce, DefaultDebounce) }

func (s *Scheduler) collectInterval() time.Duration {
	return orDefault(s.CollectInterval, DefaultCollectInterval)
}

func (s *Scheduler) watchInterval() time.Duration {
	return orDefault(s.WatchInterval, DefaultWatchInterval)
}

func (s *Scheduler) kickC() chan struct{} {
	s.kickOnce.Do(func() { s.kick = make(chan struct{}, 1) })
	return s.kick
}

// untilDayClose is the time from now to the next DayCloseDelay past midnight UTC.
func (s *Scheduler) untilDayClose(now time.Time) time.Duration {
	t := now.UTC().Truncate(24 * time.Hour).Add(orDefault(s.DayCloseDelay, DefaultDayCloseDelay))
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t.Sub(now)
}

// Start runs the cycle until ctx ends. Work still going when ctx ends is waited for.
func (s *Scheduler) Start(ctx context.Context) {
	kick := s.kickC()
	if s.Cycle.Collect != nil {
		s.collect(ctx)
	}
	_ = s.RunOnce(ctx, "start") //nolint:errcheck // RunOnce logs and records it

	fallback := time.NewTimer(s.interval())
	defer fallback.Stop()
	s.setNext(&s.st.NextRun, s.interval())
	dayClose := time.NewTimer(s.untilDayClose(time.Now()))
	defer dayClose.Stop()

	collectT := time.NewTimer(s.collectInterval())
	defer collectT.Stop()
	collectC := collectT.C
	if s.Cycle.Collect == nil {
		collectC = nil
	} else {
		s.setNext(&s.st.NextCollect, s.collectInterval())
	}
	var watchC <-chan time.Time
	if s.Cycle.Watch != nil {
		t := time.NewTicker(s.watchInterval())
		defer t.Stop()
		watchC = t.C
	}
	debounceT := time.NewTimer(time.Hour)
	debounceT.Stop()
	defer debounceT.Stop()
	var debounceC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
			if debounceC == nil {
				debounceT.Reset(s.debounce())
				debounceC = debounceT.C
			}
		case <-debounceC:
			debounceC = nil
			if reason := s.takePending(); reason != "" {
				_ = s.RunOnce(ctx, reason) //nolint:errcheck
			}
		case <-fallback.C:
			_ = s.RunOnce(ctx, "hourly check") //nolint:errcheck
			fallback.Reset(s.interval())
			s.setNext(&s.st.NextRun, s.interval())
		case <-dayClose.C:
			_ = s.RunOnce(ctx, "day closed") //nolint:errcheck
			dayClose.Reset(s.untilDayClose(time.Now()))
		case <-collectC:
			s.collect(ctx)
			_ = s.RunOnce(ctx, "collection") //nolint:errcheck
			collectT.Reset(s.collectInterval())
			s.setNext(&s.st.NextCollect, s.collectInterval())
		case <-watchC:
			if s.watch(ctx) {
				_ = s.RunOnce(ctx, "payout address sent coins") //nolint:errcheck
			}
		}
	}
}

func (s *Scheduler) setNext(field *time.Time, in time.Duration) {
	s.mu.Lock()
	*field = time.Now().Add(in).UTC()
	s.mu.Unlock()
}

// Trigger asks for a Calc within Debounce. Triggers that come before it
// starts join the same Calc.
func (s *Scheduler) Trigger(reason string) {
	s.mu.Lock()
	switch {
	case slices.Contains(s.pending, reason):
	case len(s.pending) < maxPending:
		s.pending = append(s.pending, reason)
	default:
		s.dropped++
	}
	s.st.Pending = s.pendingText()
	s.mu.Unlock()
	select {
	case s.kickC() <- struct{}{}:
	default:
	}
}

// SurveyStored triggers a Calc when the survey of pk, replacing old, can change it.
func (s *Scheduler) SurveyStored(pk string, old, new []byte) {
	changed := !bytes.Equal(old, new)
	if f := s.Cycle.SurveyChanged; f != nil {
		changed = f(old, new)
	}
	if changed {
		s.Trigger("survey " + pk)
	}
}

// pendingText is the pending reasons in one line. Caller holds mu.
func (s *Scheduler) pendingText() string {
	if len(s.pending) == 0 {
		return ""
	}
	if more := len(s.pending) - 1 + s.dropped; more > 0 {
		return fmt.Sprintf("%s and %d more", s.pending[0], more)
	}
	return s.pending[0]
}

func (s *Scheduler) takePending() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.pendingText()
	s.pending, s.dropped, s.st.Pending = nil, 0, ""
	return r
}

// RunOnce runs one Calc for reason, after any work in progress has finished.
func (s *Scheduler) RunOnce(ctx context.Context, reason string) (err error) {
	s.run.Lock()
	defer s.run.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	start := time.Now()
	s.mu.Lock()
	s.st.Running, s.st.LastStart, s.st.LastTrigger = true, start.UTC(), reason
	s.mu.Unlock()
	s.logger().WithField("trigger", reason).Info("Reward cycle starting")

	res, err := s.calc(ctx, reason)

	end := time.Now()
	s.mu.Lock()
	s.st.Running = false
	s.st.Cycles++
	s.st.LastEnd = end.UTC()
	s.st.LastDuration = end.Sub(start).Round(time.Millisecond).String()
	switch {
	case err != nil:
		s.st.Failures++
		s.st.LastResult, s.st.LastError = "failed", err.Error()
	case res.Skipped:
		s.st.Skips++
		s.st.LastResult, s.st.LastError = "skipped", ""
		s.st.LastSkip, s.st.LastSkipAt = res.Note, end.UTC()
	default:
		s.st.Recalcs++
		s.st.LastResult, s.st.LastError = "ok", ""
		s.st.LastRecalcAt = end.UTC()
	}
	if res.Date != "" {
		s.st.LastDate = res.Date
	}
	if res.InputsHash != "" {
		s.st.InputsHash = res.InputsHash
	}
	s.mu.Unlock()
	l := s.logger().WithField("trigger", reason).WithField("duration", end.Sub(start).Round(time.Millisecond))
	switch {
	case err != nil:
		l.WithError(err).Error("Reward cycle failed; it runs again at the next trigger or check")
	case res.Skipped:
		l.WithField("date", res.Date).Infof("Reward cycle skipped: %s", res.Note)
	default:
		l.WithField("date", res.Date).Info("Reward cycle done")
	}
	return err
}

// Exclusive runs fn with no Calc or Collect running. It waits for one in
// progress, and none starts until fn returns.
func (s *Scheduler) Exclusive(fn func() error) error {
	s.run.Lock()
	defer s.run.Unlock()
	return fn()
}

// calc runs Calc, turning a panic into an error so the server lives on.
func (s *Scheduler) calc(ctx context.Context, reason string) (res CalcResult, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n\n%s", p, debug.Stack())
		}
	}()
	if s.Cycle.Calc == nil {
		return res, fmt.Errorf("no reward cycle configured")
	}
	return s.Cycle.Calc(ctx, reason)
}

// collect runs Collect. A failure is logged, and the next collection tries again.
func (s *Scheduler) collect(ctx context.Context) {
	s.run.Lock()
	defer s.run.Unlock()
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	s.st.Running = true
	s.mu.Unlock()
	err := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("panic: %v\n\n%s", p, debug.Stack())
			}
		}()
		return s.Cycle.Collect(ctx)
	}()
	s.mu.Lock()
	s.st.Running = false
	s.st.LastCollect = time.Now().UTC()
	s.st.CollectError = ""
	if err != nil {
		s.st.CollectError = err.Error()
	}
	s.mu.Unlock()
	if err != nil {
		s.logger().WithError(err).Error("Reward collection failed")
	}
}

// watch runs Watch and reports whether it saw a spend.
func (s *Scheduler) watch(ctx context.Context) bool {
	changed, err := s.Cycle.Watch(ctx)
	s.mu.Lock()
	s.st.LastWatch = time.Now().UTC()
	s.st.WatchError = ""
	if err != nil {
		s.st.WatchError = err.Error()
	}
	s.mu.Unlock()
	if err != nil {
		s.logger().WithError(err).Debug("Payout address watch failed")
	}
	return changed
}

// Status returns a copy of the scheduler's state.
func (s *Scheduler) Status() CycleStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	st.Interval = s.interval().String()
	st.Debounce = s.debounce().String()
	if s.Cycle.Collect != nil {
		st.CollectInterval = s.collectInterval().String()
	}
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
	txt := "Reward cycle runs when its inputs change; next check:\n" + st.NextRun.UTC().Format(time.RFC3339)
	if st.LastResult != "" {
		txt += fmt.Sprintf("\nPrevious run: %s, %s (%s)", st.LastResult, st.LastDuration, st.LastTrigger)
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
	// DayCloseDelay is when, after midnight UTC, a day is final, DefaultDayCloseDelay when zero.
	DayCloseDelay time.Duration
}

// NewCycle returns the reward cycle for c. The skywire cli rewards package
// sets it, so a binary without that package has no cycle.
var NewCycle func(c CycleConfig) Cycle
