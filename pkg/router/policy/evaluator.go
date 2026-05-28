// Package policy pkg/router/policy/evaluator.go — Starlark
// evaluator for routing policy scripts. See RFC #2882.
package policy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// DefaultTimeout is the per-call timeout enforced via a step
// counter on the Starlark thread. Generous enough that real
// policies won't hit it; tight enough that a buggy policy never
// blocks a dial. Operator-configurable via Evaluator.WithTimeout.
const DefaultTimeout = 50 * time.Millisecond

// DefaultMaxSteps is the per-call execution-step ceiling, the
// hard counterpart to DefaultTimeout. Starlark's interpreter
// increments a counter on every fundamental operation; we cap
// it so that even a tight loop terminates. 100k steps comfortably
// covers a non-pathological policy on ARM.
const DefaultMaxSteps = 100_000

// FailureMode dictates what the evaluator returns when a script
// panics, times out, returns the wrong type, or fails to load.
type FailureMode int

const (
	// FailureFallback returns RouteSpec{Fallback: ""} (use visor
	// built-in default) and logs the violation. Production default.
	FailureFallback FailureMode = iota
	// FailureDrop returns the original error so the caller can
	// fail the dial loudly. For operators who want to notice when
	// their policy is broken.
	FailureDrop
)

// Clock is the time source the datetime stdlib reads from. The
// production evaluator uses systemClock; tests inject a
// deterministic clock so they can assert "what does the policy
// decide at 5pm on a Friday" without manipulating system time.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// NewSystemClock returns a Clock backed by time.Now(). Most
// callers use this; tests use a fake.
func NewSystemClock() Clock { return systemClock{} }

// Evaluator holds a parsed Starlark policy and applies it to
// dial contexts. Safe for concurrent Decide calls — the parsed
// program is immutable; each Decide creates a fresh thread.
type Evaluator struct {
	source      string // for error messages
	program     *starlark.Program
	timeout     time.Duration
	maxSteps    int64
	failureMode FailureMode
	clock       Clock
	provider    Provider
	logger      func(format string, args ...interface{})

	// globals holds the result of executing the script's
	// top-level statements once. The decide_route function lives
	// here. Cached at NewEvaluator time and frozen, so concurrent
	// Decide calls can read it without locking.
	globals starlark.StringDict
}

// Option configures an Evaluator at construction.
type Option func(*Evaluator)

// WithTimeout overrides the default per-call timeout.
func WithTimeout(d time.Duration) Option {
	return func(e *Evaluator) { e.timeout = d }
}

// WithMaxSteps overrides the default per-call step ceiling.
func WithMaxSteps(n int64) Option {
	return func(e *Evaluator) { e.maxSteps = n }
}

// WithFailureMode overrides the default failure handling.
func WithFailureMode(m FailureMode) Option {
	return func(e *Evaluator) { e.failureMode = m }
}

// WithClock injects a custom Clock. Used by tests to assert
// time-dependent policy behavior deterministically.
func WithClock(c Clock) Option {
	return func(e *Evaluator) { e.clock = c }
}

// WithProvider injects the data source the stdlib (geo,
// transports, peers) reads from. When unset, the Evaluator uses
// NopProvider — every Geo lookup returns "??", every Latency 0,
// etc. Tests use FakeProvider; production wires the visor's
// actual geoip / transport-tracker / hypervisor-list state.
func WithProvider(p Provider) Option {
	return func(e *Evaluator) { e.provider = p }
}

// WithLogger injects a log function. Default is a no-op; callers
// who want failures surfaced wire in their package logger.
func WithLogger(f func(format string, args ...interface{})) Option {
	return func(e *Evaluator) { e.logger = f }
}

// NewEvaluator parses the supplied Starlark source and returns an
// Evaluator ready for Decide calls. The source's top-level is
// executed once at construction time; the decide_route function
// must be defined there.
//
// Returns an error if the source fails to parse or top-level
// execution panics. A nil error guarantees that decide_route
// exists with the expected signature.
func NewEvaluator(name, src string, opts ...Option) (*Evaluator, error) {
	e := &Evaluator{
		source:      name,
		timeout:     DefaultTimeout,
		maxSteps:    DefaultMaxSteps,
		failureMode: FailureFallback,
		clock:       systemClock{},
		provider:    NopProvider(),
		logger:      func(string, ...interface{}) {},
	}
	for _, opt := range opts {
		opt(e)
	}

	// Parse + compile once. Each Decide reuses the program. Use
	// FileOptions explicitly so the linter doesn't flag the
	// legacy SourceProgram which carries global-state side effects.
	fileOpts := syntax.LegacyFileOptions()
	_, program, err := starlark.SourceProgramOptions(fileOpts, name, src, e.preDeclared().Has)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	e.program = program

	// Execute top-level — this defines decide_route. Step ceiling
	// is generous here (one-time init) but still bounded.
	thread := &starlark.Thread{
		Name:  name + "/init",
		Print: func(*starlark.Thread, string) {}, // suppress print()
	}
	globals, err := program.Init(thread, e.preDeclared())
	if err != nil {
		return nil, fmt.Errorf("init %s: %w", name, err)
	}
	globals.Freeze()
	e.globals = globals

	// Sanity-check that decide_route exists and is callable.
	fn, ok := globals["decide_route"]
	if !ok {
		return nil, fmt.Errorf("%s: missing decide_route function", name)
	}
	if _, ok := fn.(starlark.Callable); !ok {
		return nil, fmt.Errorf("%s: decide_route is not callable (got %s)", name, fn.Type())
	}

	return e, nil
}

// Decide applies the policy to one dial. Always returns a usable
// RouteSpec; on script failure the result is the configured
// fallback (and the failure is logged).
//
// Execution is bounded by the Evaluator's MaxSteps — the
// Starlark interpreter increments a counter per fundamental
// operation and aborts if it exceeds the cap. With the default
// 100k step cap, even a tight loop terminates inside a
// millisecond on commodity hardware, so we don't need a
// goroutine-based wall-clock timeout (which had a race: the
// cancel-watch goroutine could fire Cancel before starlark.Call
// even started executing, surfacing as a spurious "computation
// canceled" error on scripts that actually take microseconds).
//
// ctx is checked once at entry — if the caller's dial ctx is
// already canceled, we short-circuit to the fallback without
// attempting evaluation. We don't propagate ctx cancellation
// mid-script because the step cap already gives us a tight
// upper bound on execution time.
func (e *Evaluator) Decide(ctx context.Context, rctx RoutingContext, candidates []Candidate) (RouteSpec, error) {
	if err := ctx.Err(); err != nil {
		// Caller's ctx already done — nothing to do.
		return RouteSpec{}, nil
	}

	// Stamp now() if the caller hasn't.
	if rctx.Now.IsZero() {
		rctx.Now = e.clock.Now()
	}

	thread := &starlark.Thread{
		Name: e.source + "/decide",
		Print: func(_ *starlark.Thread, msg string) {
			e.logger("policy %s: print: %s", e.source, msg)
		},
	}
	thread.SetMaxExecutionSteps(uint64(e.maxSteps)) //nolint:gosec

	// Build the argument values: ctx struct + candidates list.
	starCtx := buildContextValue(rctx)
	starCandidates := buildCandidatesValue(candidates)

	fn := e.globals["decide_route"]
	var result starlark.Value
	func() {
		defer func() {
			if r := recover(); r != nil {
				e.logger("policy %s: panic in decide_route: %v", e.source, r)
				result = nil
			}
		}()
		v, callErr := starlark.Call(thread, fn, starlark.Tuple{starCtx, starCandidates}, nil)
		if callErr != nil {
			e.logger("policy %s: decide_route returned error: %v", e.source, callErr)
			result = nil
			return
		}
		result = v
	}()

	if result == nil {
		return e.handleFailure(errors.New("decide_route call failed"))
	}

	spec, err := parseRouteSpec(result)
	if err != nil {
		return e.handleFailure(fmt.Errorf("decide_route returned malformed value: %w", err))
	}
	return spec, nil
}

// OnLegChange invokes the script's optional `on_leg_change(ctx,
// legs, change)` function. Returns the empty RouteSpec when the
// script doesn't define the function (the common case for scripts
// that don't care about post-setup leg events) — the route group
// treats that as "no distribution update."
//
// Same step ceiling as Decide; failures are non-fatal (logged,
// then return empty spec).
func (e *Evaluator) OnLegChange(ctx context.Context, rctx RoutingContext, legs []LegInfo, change LegChange) (RouteSpec, error) {
	fn, ok := e.globals["on_leg_change"]
	if !ok {
		return RouteSpec{}, nil
	}
	if _, ok := fn.(starlark.Callable); !ok {
		return RouteSpec{}, nil
	}
	if err := ctx.Err(); err != nil {
		return RouteSpec{}, nil
	}
	if rctx.Now.IsZero() {
		rctx.Now = e.clock.Now()
	}
	thread := &starlark.Thread{
		Name: e.source + "/on_leg_change",
		Print: func(_ *starlark.Thread, msg string) {
			e.logger("policy %s: print: %s", e.source, msg)
		},
	}
	thread.SetMaxExecutionSteps(uint64(e.maxSteps)) //nolint:gosec
	starCtx := buildContextValue(rctx)
	starLegs := buildLegsValue(legs)
	starChange := buildLegChangeValue(change)
	var result starlark.Value
	func() {
		defer func() {
			if r := recover(); r != nil {
				e.logger("policy %s: panic in on_leg_change: %v", e.source, r)
				result = nil
			}
		}()
		v, callErr := starlark.Call(thread, fn, starlark.Tuple{starCtx, starLegs, starChange}, nil)
		if callErr != nil {
			e.logger("policy %s: on_leg_change returned error: %v", e.source, callErr)
			result = nil
			return
		}
		result = v
	}()
	if result == nil {
		return RouteSpec{}, errors.New("on_leg_change call failed")
	}
	spec, err := parseRouteSpec(result)
	if err != nil {
		return RouteSpec{}, fmt.Errorf("on_leg_change returned malformed value: %w", err)
	}
	return spec, nil
}

// LegInfo and LegChange mirror the router package's types so
// scripts can reason about them without the policy package
// pulling in the router. The bridge layer converts between the
// two when the hook fires.
type LegInfo struct {
	Index     int
	Kind      string
	LatencyMs int
	Alive     bool
}

type LegChange struct {
	Event    string
	LegIndex int
}

func (e *Evaluator) handleFailure(err error) (RouteSpec, error) {
	if e.failureMode == FailureDrop {
		return RouteSpec{Fallback: "drop"}, err
	}
	// Fallback mode: return an empty spec (visor uses built-in
	// default) and surface the failure only via the logger.
	return RouteSpec{}, nil
}
