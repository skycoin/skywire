// Package wasm pkg/router/policy/wasm/evaluator.go — wazero-
// backed Evaluator that satisfies the same shape as the
// Starlark Evaluator. The router-side Loader can plug either in.
//
// The guest module must export:
//   - alloc(size: i32) → ptr: i32      // host-driven malloc
//   - free(ptr: i32, size: i32)        // host-driven free
//   - decide_route(in_ptr: i32, in_len: i32) → packed: i64
//                                       // packed = ptr | len<<32
//
// Optional:
//   - on_leg_change(in_ptr: i32, in_len: i32) → packed: i64
//
// Modules are stateless across decide_route calls — the host
// instantiates one module per script load (with hot-reload
// re-instantiating cleanly) and calls Decide concurrently.
// Wazero modules are NOT goroutine-safe; we serialize calls
// via a sync.Mutex. This is acceptable because the WASM call
// is sub-µs for typical TinyGo policies.
package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/skycoin/skywire/pkg/router/policy"
)

// Clock mirrors policy.Clock so the wasm evaluator can be
// driven by an injected clock in tests.
type Clock interface {
	Now() time.Time
}

// Evaluator wraps a compiled wazero module + an instantiated
// module instance.
type Evaluator struct {
	source   string
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	hostEnv  *hostEnv
	clock    Clock
	maxSteps int64
	logger   func(format string, args ...interface{})

	mu           sync.Mutex
	instance     api.Module
	exportDecide api.Function
	exportLeg    api.Function
	exportTick   api.Function
	exportAlloc  api.Function
}

// Option configures an Evaluator at construction.
type Option func(*Evaluator)

// WithProvider injects the data Provider for the host stdlib.
func WithProvider(p policy.Provider) Option {
	return func(e *Evaluator) { e.hostEnv.provider = p }
}

// WithLogger overrides the default no-op logger.
func WithLogger(f func(format string, args ...interface{})) Option {
	return func(e *Evaluator) {
		e.logger = f
		e.hostEnv.logger = f
	}
}

// WithClock injects a Clock for ctx.NowUnixNano stamping.
func WithClock(c Clock) Option {
	return func(e *Evaluator) { e.clock = c }
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// NewSystemClock returns a Clock backed by time.Now.
func NewSystemClock() Clock { return systemClock{} }

// NewEvaluator compiles the supplied WASM module bytes and
// returns a ready-to-use Evaluator. On parse / instantiation
// failure returns an error.
func NewEvaluator(name string, wasmBytes []byte, opts ...Option) (*Evaluator, error) {
	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))

	env := &hostEnv{
		source: name,
		logger: func(string, ...interface{}) {},
	}
	e := &Evaluator{
		source:   name,
		runtime:  rt,
		hostEnv:  env,
		clock:    systemClock{},
		maxSteps: policy.DefaultMaxSteps,
		logger:   func(string, ...interface{}) {},
	}
	for _, opt := range opts {
		opt(e)
	}

	// WASI imports required by TinyGo's wasi target — without
	// this, the guest module fails to instantiate because
	// TinyGo's runtime expects `wasi_snapshot_preview1` to be
	// available.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx) //nolint:errcheck
		return nil, fmt.Errorf("wasm: instantiate wasi: %w", err)
	}

	builder := rt.NewHostModuleBuilder("skywire")
	installHostFunctions(ctx, env, builder)
	if _, err := builder.Instantiate(ctx); err != nil {
		_ = rt.Close(ctx) //nolint:errcheck
		return nil, fmt.Errorf("wasm: instantiate host module: %w", err)
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx) //nolint:errcheck
		return nil, fmt.Errorf("wasm: compile %s: %w", name, err)
	}
	e.compiled = compiled

	// Suppress automatic _start so TinyGo's main() doesn't call
	// proc_exit and close the module before we get to call the
	// guest's policy exports. The guest's main() runs as part of
	// _start invocation; setting StartFunctions to nothing means
	// the module stays "alive" for subsequent calls into
	// decide_route / on_leg_change / alloc.
	instance, err := rt.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().
			WithName(name).
			WithStartFunctions())
	if err != nil {
		_ = rt.Close(ctx) //nolint:errcheck
		return nil, fmt.Errorf("wasm: instantiate %s: %w", name, err)
	}
	// TinyGo modules need _initialize (the "reactor" entry
	// point) to set up globals + the heap. _start runs main()
	// then exits; _initialize runs initializers without main().
	// Both are guarded behind WithStartFunctions() above.
	if initFn := instance.ExportedFunction("_initialize"); initFn != nil {
		if _, err := initFn.Call(ctx); err != nil {
			_ = rt.Close(ctx) //nolint:errcheck
			return nil, fmt.Errorf("wasm: %s _initialize: %w", name, err)
		}
	}
	e.instance = instance
	e.exportDecide = instance.ExportedFunction("decide_route")
	if e.exportDecide == nil {
		_ = rt.Close(ctx) //nolint:errcheck
		return nil, fmt.Errorf("wasm: %s missing exported decide_route", name)
	}
	e.exportLeg = instance.ExportedFunction("on_leg_change") // optional
	e.exportTick = instance.ExportedFunction("on_tick")      // optional
	e.exportAlloc = instance.ExportedFunction("alloc")
	if e.exportAlloc == nil {
		_ = rt.Close(ctx) //nolint:errcheck
		return nil, fmt.Errorf("wasm: %s missing exported alloc", name)
	}

	return e, nil
}

// Close releases the wazero runtime + compiled module. Safe to
// call concurrently with Decide; calls after Close return an
// error from the next Decide.
func (e *Evaluator) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime != nil {
		_ = e.runtime.Close(context.Background()) //nolint:errcheck
		e.runtime = nil
	}
	return nil
}

// Decide marshals (rctx, candidates) to JSON, calls the guest's
// decide_route, and unmarshals the returned RouteSpec.
func (e *Evaluator) Decide(ctx context.Context, rctx policy.RoutingContext, candidates []policy.Candidate) (policy.RouteSpec, error) {
	if err := ctx.Err(); err != nil {
		return policy.RouteSpec{}, nil
	}
	if rctx.Now.IsZero() {
		rctx.Now = e.clock.Now()
	}
	input := DecideInputWire{
		Ctx:        wireFromCtx(rctx),
		Candidates: wireFromCandidates(candidates),
	}
	out, err := e.call(ctx, e.exportDecide, "decide_route", input)
	if err != nil {
		return policy.RouteSpec{}, err
	}
	return specFromWire(out), nil
}

// OnTick invokes the optional on_tick export with the current
// leg snapshot. Returns the zero-value RotationAction (no-op)
// when the module doesn't export on_tick. Errors fall through
// as zero-action so a misbehaving guest can't drop legs
// unexpectedly.
func (e *Evaluator) OnTick(ctx context.Context, rctx policy.RoutingContext, legs []policy.LegInfo) (policy.RotationAction, error) {
	if e.exportTick == nil {
		return policy.RotationAction{}, nil
	}
	if err := ctx.Err(); err != nil {
		return policy.RotationAction{}, nil
	}
	if rctx.Now.IsZero() {
		rctx.Now = e.clock.Now()
	}
	input := TickInputWire{
		Ctx:  wireFromCtx(rctx),
		Legs: wireFromLegs(legs),
	}
	wire, err := e.callRotation(ctx, e.exportTick, "on_tick", input)
	if err != nil {
		return policy.RotationAction{}, err
	}
	return rotationFromWire(wire), nil
}

// callRotation is the OnTick variant of call: same marshal-call-
// unmarshal flow but decodes RotationActionWire instead of
// RouteSpecWire.
func (e *Evaluator) callRotation(ctx context.Context, fn api.Function, name string, input any) (RotationActionWire, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		return RotationActionWire{}, errors.New("wasm: evaluator closed")
	}
	jsonIn, err := json.Marshal(input)
	if err != nil {
		return RotationActionWire{}, fmt.Errorf("wasm: marshal %s input: %w", name, err)
	}
	allocRes, err := e.exportAlloc.Call(ctx, uint64(len(jsonIn)))
	if err != nil || len(allocRes) == 0 {
		return RotationActionWire{}, fmt.Errorf("wasm: alloc for %s failed: %v", name, err)
	}
	inPtr := uint32(allocRes[0])
	if ok := e.instance.Memory().Write(inPtr, jsonIn); !ok {
		return RotationActionWire{}, fmt.Errorf("wasm: write %s input to memory failed", name)
	}
	res, err := fn.Call(ctx, uint64(inPtr), uint64(len(jsonIn)))
	if err != nil {
		return RotationActionWire{}, fmt.Errorf("wasm: %s call failed: %w", name, err)
	}
	if len(res) == 0 {
		return RotationActionWire{}, nil
	}
	outPtr, outLen := unpackPtrLen(res[0])
	if outLen == 0 {
		return RotationActionWire{}, nil
	}
	jsonOut, ok := e.instance.Memory().Read(outPtr, outLen)
	if !ok {
		return RotationActionWire{}, fmt.Errorf("wasm: read %s output failed", name)
	}
	var action RotationActionWire
	if err := json.Unmarshal(jsonOut, &action); err != nil {
		return RotationActionWire{}, fmt.Errorf("wasm: unmarshal %s output: %w", name, err)
	}
	return action, nil
}

func rotationFromWire(w RotationActionWire) policy.RotationAction {
	return policy.RotationAction{
		DropLegs:    append([]int(nil), w.DropLegs...),
		AddLeg:      w.AddLeg,
		ExcludeHops: append([]string(nil), w.ExcludeHops...),
	}
}

// OnLegChange invokes the optional on_leg_change export. Returns
// an empty spec when the module doesn't export it.
func (e *Evaluator) OnLegChange(ctx context.Context, rctx policy.RoutingContext, legs []policy.LegInfo, change policy.LegChange) (policy.RouteSpec, error) {
	if e.exportLeg == nil {
		return policy.RouteSpec{}, nil
	}
	if err := ctx.Err(); err != nil {
		return policy.RouteSpec{}, nil
	}
	if rctx.Now.IsZero() {
		rctx.Now = e.clock.Now()
	}
	input := LegChangeInputWire{
		Ctx:    wireFromCtx(rctx),
		Legs:   wireFromLegs(legs),
		Change: LegChangeWire{Event: change.Event, LegIndex: change.LegIndex},
	}
	return e.callExport(ctx, e.exportLeg, "on_leg_change", input)
}

// call is the shared host-to-guest invocation: marshal input to
// JSON, alloc a guest buffer, write into it, call the function,
// read the returned packed (ptr|len), unmarshal the JSON
// RouteSpec.
func (e *Evaluator) call(ctx context.Context, fn api.Function, name string, input any) (RouteSpecWire, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		return RouteSpecWire{}, errors.New("wasm: evaluator closed")
	}
	jsonIn, err := json.Marshal(input)
	if err != nil {
		return RouteSpecWire{}, fmt.Errorf("wasm: marshal %s input: %w", name, err)
	}
	allocRes, err := e.exportAlloc.Call(ctx, uint64(len(jsonIn)))
	if err != nil || len(allocRes) == 0 {
		return RouteSpecWire{}, fmt.Errorf("wasm: alloc for %s failed: %v", name, err)
	}
	inPtr := uint32(allocRes[0])
	if ok := e.instance.Memory().Write(inPtr, jsonIn); !ok {
		return RouteSpecWire{}, fmt.Errorf("wasm: write %s input to memory failed", name)
	}
	res, err := fn.Call(ctx, uint64(inPtr), uint64(len(jsonIn)))
	if err != nil {
		return RouteSpecWire{}, fmt.Errorf("wasm: %s call failed: %w", name, err)
	}
	if len(res) == 0 {
		return RouteSpecWire{}, nil
	}
	outPtr, outLen := unpackPtrLen(res[0])
	if outLen == 0 {
		return RouteSpecWire{}, nil
	}
	jsonOut, ok := e.instance.Memory().Read(outPtr, outLen)
	if !ok {
		return RouteSpecWire{}, fmt.Errorf("wasm: read %s output failed", name)
	}
	var spec RouteSpecWire
	if err := json.Unmarshal(jsonOut, &spec); err != nil {
		return RouteSpecWire{}, fmt.Errorf("wasm: unmarshal %s output: %w", name, err)
	}
	return spec, nil
}

// callExport is the OnLegChange variant — same shape but
// returns the projected policy.RouteSpec directly.
func (e *Evaluator) callExport(ctx context.Context, fn api.Function, name string, input any) (policy.RouteSpec, error) {
	wire, err := e.call(ctx, fn, name, input)
	if err != nil {
		return policy.RouteSpec{}, err
	}
	return specFromWire(wire), nil
}

// wireFromCtx flattens a policy.RoutingContext to its wire shape.
func wireFromCtx(r policy.RoutingContext) RoutingContextWire {
	return RoutingContextWire{
		App:               r.App,
		PeerPK:            r.PeerPK,
		Port:              r.Port,
		NowUnixNano:       r.Now.UnixNano(),
		CLIOverrides:      r.CLIOverrides,
		IsDirectDial:      r.IsDirectDial,
		TransportKind:     r.TransportKind,
		ReverseCandidates: wireFromCandidates(r.ReverseCandidates),
	}
}

func wireFromCandidates(cs []policy.Candidate) []CandidateWire {
	out := make([]CandidateWire, len(cs))
	for i, c := range cs {
		out[i] = CandidateWire{
			Hops:           c.Hops,
			HopsGeo:        c.HopsGeo,
			EstLatencyMs:   c.EstLatencyMs,
			TransportKinds: c.TransportKinds,
		}
	}
	return out
}

func wireFromLegs(legs []policy.LegInfo) []LegInfoWire {
	out := make([]LegInfoWire, len(legs))
	for i, l := range legs {
		out[i] = LegInfoWire{Index: l.Index, Kind: l.Kind, LatencyMs: l.LatencyMs, Alive: l.Alive}
	}
	return out
}

func specFromWire(w RouteSpecWire) policy.RouteSpec {
	out := policy.RouteSpec{
		Mux:                     w.Mux,
		ForwardMux:              w.ForwardMux,
		ReverseMux:              w.ReverseMux,
		MinHops:                 w.MinHops,
		ForwardMinHops:          w.ForwardMinHops,
		ReverseMinHops:          w.ReverseMinHops,
		Fallback:                w.Fallback,
		Distribution:            w.Distribution,
		RotationIntervalSeconds: w.RotationIntervalSeconds,
		AvoidDirect:             w.AvoidDirect,
	}
	if w.Chosen != nil {
		c := candidateFromWire(*w.Chosen)
		out.Chosen = &c
	}
	if w.ReverseChosen != nil {
		c := candidateFromWire(*w.ReverseChosen)
		out.ReverseChosen = &c
	}
	return out
}

func candidateFromWire(w CandidateWire) policy.Candidate {
	return policy.Candidate{
		Hops:           w.Hops,
		HopsGeo:        w.HopsGeo,
		EstLatencyMs:   w.EstLatencyMs,
		TransportKinds: w.TransportKinds,
	}
}
