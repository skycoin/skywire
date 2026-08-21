// Package rpcgrpc pkg/visor/rpcgrpc/server_policy_bandwidth.go c3-vis-core
// the `policy-bw` control layer that drives the in-process mux-bandwidth rig
// through the WASM / Starlark routing-policy engine — proving a preset is
// demonstrably ADAPTIVE over a live multiplexed route.
//
// Where the pure mux-bw baseline (server_mux_bandwidth.go) plans a fixed set of
// disjoint routes and pumps them unchanged for the whole run, this layer:
//
//   - at dial time, calls the engine's decide_route hook to pick the mux leg
//     count + min-hops (muxBwController.decideAtDial), then reuses the same
//     disjoint planner + setup path; and
//   - during the pump, on the preset's rotation cadence, fires the engine's
//     on_tick hook against the live per-leg stats and applies the returned
//     RotationAction — dropping / adding legs and promoting / demoting warm
//     standbys (muxBwController.controlLoop → applyRotation).
//
// The leg-management surface is the in-process analog of the route group's
// RotationAction executor (pkg/router/route_group.go rotationServiceFn): a drop
// cancels one leg's pump + tears down its conn; an add plans a fresh disjoint
// route and spawns a pump; a demote parks a leg on warm standby (conn kept
// alive via a keepalive ping, not byte-pumped) and a promote resumes it
// instantly with no re-dial. Route-group LegInfo semantics are preserved so a
// preset written against the app path behaves identically here: leg indices are
// slice positions that compact after a drop, and each leg's TransportID (the
// key a preset uses to smooth an EWMA across ticks) is its stable intermediate
// PK.
//
// Concurrency: active/all and the sampler tracker are touched ONLY by the
// single controlLoop goroutine (after a happens-before init in startPumps), so
// they need no lock; the pump goroutines share state only through the per-leg
// atomics on muxBwRouteState. totals()/peaks() are read after pumpWg.Wait(),
// i.e. once controlLoop has returned.
package rpcgrpc

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/router/policy"
	policywasm "github.com/skycoin/skywire/pkg/router/policy/wasm"
	wasmpresets "github.com/skycoin/skywire/pkg/router/policy/wasm/presets"
)

// muxBwDefaultTickInterval is the on_tick cadence used when the preset's
// decide_route did not set rotation_interval_seconds. The on_tick presets set
// it themselves (rotating-bw=90s, latency-adaptive=30s, …); this only covers a
// policy that exports on_tick without declaring a cadence.
const muxBwDefaultTickInterval = 10 * time.Second

// muxBwStandbyKeepalive is how often a warm-standby leg sends a small keepalive
// ping to hold its conn open (and refresh its latency EWMA) while parked out of
// the pump set.
const muxBwStandbyKeepalive = 3 * time.Second

// loadMuxBwPolicyEngine resolves a routing.policy_per_dial-style value into a
// policy.Engine, mirroring pkg/visor/policy_loader.go: "preset:<name>" prefers
// the compiled WASM bundle when the name is a bundle preset, else falls through
// to the Starlark preset registry; a "@*.wasm" path loads a WASM module; any
// other value (@*.star / inline) loads the Starlark backend.
func loadMuxBwPolicyEngine(raw string, logger func(string, ...interface{})) (policy.Engine, error) {
	prov := policy.NopProvider()
	if name, ok := strings.CutPrefix(raw, "preset:"); ok {
		if wasmpresets.Has(name) {
			return policywasm.NewLoaderBytes(name, wasmpresets.Bundle(),
				policywasm.WithPreset(name),
				policywasm.WithLogger(logger),
				policywasm.WithProvider(prov))
		}
		// Not a bundle preset — let the Starlark loader resolve preset:<name>
		// from its registry (or error on an unknown name).
	}
	if strings.HasPrefix(raw, "@") &&
		strings.EqualFold(filepath.Ext(strings.TrimPrefix(raw, "@")), ".wasm") {
		return policywasm.NewLoader(raw,
			policywasm.WithLogger(logger),
			policywasm.WithProvider(prov))
	}
	return policy.NewLoader(raw,
		policy.WithLogger(logger),
		policy.WithProvider(prov))
}

// muxBwController owns the policy-driven leg set for one policy-bw run.
type muxBwController struct {
	s      *PingServer
	cfg    *muxBwCfg
	engine policy.Engine
	emit   func(isMuxBandwidthEvent_Payload)

	// tickInterval is the on_tick cadence, set by decideAtDial from the
	// preset's rotation_interval_seconds (or the default).
	tickInterval time.Duration

	// active is the current leg set — the slice on_tick sees (Index = position,
	// compacted on drop). all is append-only: every leg ever created, so the
	// Done totals include the bytes of legs later dropped. Both touched only by
	// the controlLoop goroutine.
	active []*muxBwRouteState
	all    []*muxBwRouteState

	tracker   *muxBwSampleTracker
	nextIndex int // monotonic RouteIndex for added legs (conn-map key)

	pumpWg *sync.WaitGroup // shared with StreamMuxBandwidth's pump WaitGroup
}

// newMuxBwController loads the policy engine and returns a controller. The
// engine's Decide/OnTick are invoked later (decideAtDial / controlLoop).
func (s *PingServer) newMuxBwController(cfg *muxBwCfg, emit func(isMuxBandwidthEvent_Payload)) (*muxBwController, error) {
	eng, err := loadMuxBwPolicyEngine(cfg.RoutingPolicy, s.log.Debugf)
	if err != nil {
		return nil, fmt.Errorf("routing policy %q: %w", cfg.RoutingPolicy, err)
	}
	return &muxBwController{
		s:            s,
		cfg:          cfg,
		engine:       eng,
		emit:         emit,
		tickInterval: muxBwDefaultTickInterval,
		// Added legs start above the pump-route indices AND the dedicated
		// probe route (index == cfg.Routes), so a fresh conn key never
		// collides with a still-live route.
		nextIndex: cfg.Routes + 1,
	}, nil
}

func (c *muxBwController) close() {
	if c.engine != nil {
		_ = c.engine.Close() //nolint:errcheck
	}
}

// routingContext builds the RoutingContext the engine sees. App drives the
// preset's per-app branching; CLIOverrides surface the operator's leg-count /
// min-hops so a preset can honor or override them, plus any ad-hoc
// `--override key=value` flags — the map the conditional presets read
// (geo-avoid=avoid_geo, trust-tiered=trusted_pks, time-of-day=business_hours).
func (c *muxBwController) routingContext() policy.RoutingContext {
	overrides := map[string]string{}
	if c.cfg.Routes > 0 {
		overrides["mux_routes"] = strconv.Itoa(c.cfg.Routes)
	}
	if c.cfg.MinHops > 0 {
		overrides["min_hops"] = strconv.Itoa(c.cfg.MinHops)
	}
	// Operator-supplied `--override key=value` pairs land last so an explicit
	// flag wins over the handler-derived mux_routes / min_hops entries. Nil map
	// (the baseline / no --override) is a no-op, so every preset keeps its
	// built-in defaults.
	for k, v := range c.cfg.CliOverrides {
		overrides[k] = v
	}
	return policy.RoutingContext{
		App:          c.cfg.PolicyApp,
		PeerPK:       c.cfg.TargetPK.Hex(),
		CLIOverrides: overrides,
	}
}

// decideAtDial runs the engine's decide_route hook and folds the result into
// cfg: the mux leg count (max of Mux / ForwardMux / ReverseMux — the rig pumps
// symmetric echo traffic) and min-hops become the setup targets, and the
// rotation cadence drives the on_tick loop. A nil/empty spec leaves the
// operator's flags in force, so a static preset is a no-op override.
func (c *muxBwController) decideAtDial(ctx context.Context) {
	spec, err := c.engine.Decide(ctx, c.routingContext(), nil)
	if err != nil {
		c.s.log.Debugf("policy-bw: decide_route failed (%v); keeping operator flags", err)
		return
	}
	mux := spec.Mux
	if spec.ForwardMux > mux {
		mux = spec.ForwardMux
	}
	if spec.ReverseMux > mux {
		mux = spec.ReverseMux
	}
	if mux > 0 {
		c.cfg.Routes = mux
	}
	if spec.MinHops > 0 {
		c.cfg.MinHops = spec.MinHops
	}
	if spec.RotationIntervalSeconds > 0 {
		c.tickInterval = time.Duration(spec.RotationIntervalSeconds) * time.Second
	}
	c.s.log.Debugf("policy-bw: decide_route → routes=%d min_hops=%d tick=%s",
		c.cfg.Routes, c.cfg.MinHops, c.tickInterval)
}

// startPumps seeds the leg set from the established routes and spawns a pump
// goroutine per leg. Called once, from the main goroutine, before controlLoop
// starts (the `go` that follows establishes happens-before on active/all).
func (c *muxBwController) startPumps(pumpCtx context.Context, routes []*muxBwRouteState, pumpStart time.Time, wg *sync.WaitGroup) {
	c.pumpWg = wg
	c.tracker = newMuxBwSampleTracker(pumpStart)
	for _, r := range routes {
		if !r.established.Load() {
			continue
		}
		c.all = append(c.all, r)
		c.active = append(c.active, r)
		r.activeFlag.Store(true)
		c.spawnPump(pumpCtx, r, pumpStart)
	}
}

// spawnPump starts one leg's pump goroutine with its own cancel so a drop can
// stop just this leg. Registers on the shared pump WaitGroup.
func (c *muxBwController) spawnPump(pumpCtx context.Context, rs *muxBwRouteState, pumpStart time.Time) {
	legCtx, cancel := context.WithCancel(pumpCtx)
	rs.pumpCancel = cancel
	c.pumpWg.Add(1)
	go func() {
		defer c.pumpWg.Done()
		defer rs.activeFlag.Store(false)
		c.pumpLeg(legCtx, rs, pumpStart)
	}()
}

// pumpLeg is the policy-aware pump: an ACTIVE leg bulk-pumps echo bytes
// (accumulating the per-leg counters + latency EWMA); a warm-STANDBY leg only
// sends a small keepalive ping to hold its conn open (and refresh its EWMA)
// without contributing bandwidth — the in-process warm-standby gate. Flipping
// rs.standby (from applyRotation) switches modes on the next iteration with no
// re-dial. The defer tears down just this leg's conn.
func (c *muxBwController) pumpLeg(ctx context.Context, rs *muxBwRouteState, pumpStart time.Time) {
	defer func() {
		_ = c.s.visor.StopPingRoute(PingRouteRef{PK: c.cfg.TargetPK, Index: rs.index}) //nolint:errcheck
	}()
	pumpConf := PingConf{
		PK:         c.cfg.TargetPK,
		Tries:      1,
		PcktSize:   c.cfg.PacketSizeKb,
		LocalRoute: c.cfg.LocalRoute,
		RouteIndex: rs.index,
	}
	kaConf := pumpConf
	kaConf.PcktSize = 1
	kaConf.Timeout = muxBwProbeTimeout

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if rs.standby.Load() {
			// Warm standby: keepalive only, no bandwidth. Latency from the
			// small round-trip keeps the EWMA fresh so a promotion decision
			// has a current signal.
			lat, err := c.s.visor.PingOnce(kaConf)
			if err == nil {
				rs.updateLatencyEWMA(lat)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(muxBwStandbyKeepalive):
			}
			continue
		}
		bytesSent, bytesRecvd, latency, err := c.s.visor.PingOnceWithEcho(pumpConf, true)
		if err != nil {
			if ctx.Err() == nil || rs.bytesSent.Load() == 0 {
				c.emit(&MuxBandwidthEvent_RouteFailure{
					RouteFailure: buildRouteFailureEvent(rs, err, pumpStart),
				})
			}
			c.s.log.Debugf("policy-bw: leg %d pump error: %v", rs.index, err)
			return
		}
		rs.bytesSent.Add(bytesSent)
		rs.bytesRecv.Add(bytesRecvd)
		rs.updateLatencyEWMA(latency)
	}
}

// controlLoop drives the run: it emits the periodic MuxBandwidthSample on the
// sample cadence and fires the engine's on_tick hook on the (usually slower)
// rotation cadence, applying the returned RotationAction. Runs as a single
// goroutine, so it is the sole mutator of active/all + tracker.
func (c *muxBwController) controlLoop(pumpCtx context.Context, pumpStart time.Time) {
	sampleTick := time.NewTicker(c.cfg.SampleInterval)
	defer sampleTick.Stop()
	tickInterval := c.tickInterval
	if tickInterval <= 0 {
		tickInterval = muxBwDefaultTickInterval
	}
	policyTick := time.NewTicker(tickInterval)
	defer policyTick.Stop()

	for {
		select {
		case <-pumpCtx.Done():
			return
		case <-sampleTick.C:
			sample := c.tracker.sample(c.active, time.Now(), pumpStart, true)
			c.emit(&MuxBandwidthEvent_Sample{Sample: sample})
		case <-policyTick.C:
			c.onTick(pumpCtx, pumpStart)
		}
	}
}

// snapshotLegs builds the []policy.LegInfo on_tick sees from the current active
// set. Index is the slice position (route-group semantics: not stable across
// ticks — a drop compacts). TransportID is the leg's stable intermediate PK so
// a preset can key an EWMA per leg across ticks (falls back to the route index
// for a direct leg with no intermediate).
func (c *muxBwController) snapshotLegs() []policy.LegInfo {
	legs := make([]policy.LegInfo, 0, len(c.active))
	for i, r := range c.active {
		tid := r.intermediatePK
		if tid == "" {
			tid = "idx:" + strconv.Itoa(r.index)
		}
		hops := make([]string, 0, len(r.fwdHops))
		for _, h := range r.fwdHops {
			if h.To != "" {
				hops = append(hops, h.To)
			}
		}
		legs = append(legs, policy.LegInfo{
			Index:       i,
			Kind:        r.transportKind,
			TransportID: tid,
			LatencyMs:   r.latencyMs(),
			Alive:       r.activeFlag.Load(),
			Standby:     r.standby.Load(),
			SentBytes:   r.bytesSent.Load(),
			RecvBytes:   r.bytesRecv.Load(),
			Hops:        hops,
		})
	}
	return legs
}

// onTick fires the engine's on_tick hook and applies the RotationAction.
func (c *muxBwController) onTick(pumpCtx context.Context, pumpStart time.Time) {
	legs := c.snapshotLegs()
	if len(legs) == 0 {
		return
	}
	action, err := c.engine.OnTick(pumpCtx, c.routingContext(), legs)
	if err != nil {
		c.s.log.Debugf("policy-bw: on_tick error: %v", err)
		return
	}
	c.applyRotation(pumpCtx, action, pumpStart)
}

// applyRotation applies one RotationAction against the active leg set, in the
// SAME order as the route group's executor (pkg/router/route_group.go): promote
// / demote first (they only flip the standby gate, so they run on the pre-drop
// indices), then drops (which compact the slice), then a best-effort add. All
// indices are positions into the snapshot the hook just saw.
func (c *muxBwController) applyRotation(pumpCtx context.Context, action policy.RotationAction, pumpStart time.Time) {
	elapsed := func() int64 { return time.Since(pumpStart).Nanoseconds() }

	// Promote first, then demote — mirrors route_group.go so a policy that
	// promotes a spare and demotes an active in the same tick keeps a
	// selectable leg throughout.
	for _, idx := range action.PromoteFromStandby {
		if r := c.legAt(idx); r != nil && r.standby.Load() {
			r.standby.Store(false)
			c.emitLifecycle("promoted", r, "active", elapsed())
		}
	}
	for _, idx := range action.DemoteToStandby {
		// Never park the last active (non-standby) leg — a run must always
		// have a pumping leg, matching routeMux's "leg 0 is never standby".
		if r := c.legAt(idx); r != nil && !r.standby.Load() && c.activePumpCount() > 1 {
			r.standby.Store(true)
			c.emitLifecycle("demoted", r, "standby", elapsed())
		}
	}

	// Drops: cancel each leg's pump (its defer tears down the conn) and remove
	// it from active so subsequent snapshots re-index. Never drop the last
	// alive leg — mirrors route_group.go dropLegsByIndex: precompute the alive
	// count and decrement per drop, and build a FRESH kept slice so the alive
	// bookkeeping can't alias the compaction.
	if len(action.DropLegs) > 0 {
		toDrop := map[int]struct{}{}
		for _, idx := range action.DropLegs {
			if idx >= 0 && idx < len(c.active) {
				toDrop[idx] = struct{}{}
			}
		}
		alive := c.aliveCount()
		kept := make([]*muxBwRouteState, 0, len(c.active))
		for i, r := range c.active {
			if _, drop := toDrop[i]; drop && r.activeFlag.Load() && alive > 1 {
				if r.pumpCancel != nil {
					r.pumpCancel()
				}
				r.activeFlag.Store(false)
				alive--
				c.emitLifecycle("dropped", r, "dropped", elapsed())
				continue
			}
			kept = append(kept, r)
		}
		c.active = kept
	}

	if action.AddLeg {
		c.addLeg(pumpCtx, action.ExcludeHops, pumpStart)
	}
}

// legAt returns the active leg at snapshot position idx, or nil if out of range
// (a stale index from a snapshot the mutation already invalidated).
func (c *muxBwController) legAt(idx int) *muxBwRouteState {
	if idx < 0 || idx >= len(c.active) {
		return nil
	}
	return c.active[idx]
}

// aliveCount counts active-set legs whose pump goroutine is still running.
func (c *muxBwController) aliveCount() int {
	n := 0
	for _, r := range c.active {
		if r.activeFlag.Load() {
			n++
		}
	}
	return n
}

// activePumpCount counts active-set legs that are actually pumping (alive and
// not parked on warm standby) — the count a demote must keep >= 1.
func (c *muxBwController) activePumpCount() int {
	n := 0
	for _, r := range c.active {
		if r.activeFlag.Load() && !r.standby.Load() {
			n++
		}
	}
	return n
}

// addLeg plans one fresh disjoint route whose intermediates avoid both the
// current legs' intermediates and the policy-supplied excludeHops, sets it up,
// and — on success — appends it to the leg set and spawns its pump. Best-effort:
// a planning / setup miss is logged and leaves the leg set unchanged, matching
// the route group's add-leg semantics.
func (c *muxBwController) addLeg(pumpCtx context.Context, excludeHops []string, pumpStart time.Time) {
	exclude := map[string]struct{}{}
	for _, h := range excludeHops {
		exclude[h] = struct{}{}
	}
	for _, r := range c.active {
		if r.intermediatePK != "" {
			exclude[r.intermediatePK] = struct{}{}
		}
	}

	// Ask for more candidates than we have legs so at least one avoids the
	// exclude set even after the planner drops overlapping ones.
	want := len(c.active) + len(exclude) + 2
	plans, err := c.s.muxBwPlanDisjointRoutes(pumpCtx, c.cfg.TargetPK, c.cfg.MinHops, defaultMuxBwMaxHops, want)
	if err != nil || len(plans) == 0 {
		c.s.log.Debugf("policy-bw: add-leg planning yielded nothing (%v); keeping leg set", err)
		return
	}
	var chosen *muxBwRoutePlan
	for i := range plans {
		p := &plans[i]
		if len(p.forward) == 0 {
			continue
		}
		overlap := false
		for _, h := range p.forward {
			if _, bad := exclude[h.To]; bad && h.To != "" {
				overlap = true
				break
			}
		}
		if !overlap {
			chosen = p
			break
		}
	}
	if chosen == nil {
		c.s.log.Debugf("policy-bw: add-leg found no disjoint path; keeping leg set")
		return
	}

	rs := &muxBwRouteState{index: c.nextIndex, fwdHops: chosen.forward, revHops: chosen.reverse}
	c.nextIndex++
	c.s.muxBwSetupRoute(pumpCtx, c.cfg, rs, muxBwDropRouteEstablished(c.emit))
	if !rs.established.Load() {
		c.s.log.Debugf("policy-bw: add-leg setup failed for index %d; keeping leg set", rs.index)
		return
	}
	rs.activeFlag.Store(true)
	c.all = append(c.all, rs)
	c.active = append(c.active, rs)
	c.spawnPump(pumpCtx, rs, pumpStart)
	c.emitLifecycle("added", rs, "active", time.Since(pumpStart).Nanoseconds())
}

// emitLifecycle emits a MuxLegLifecycle event onto the stream.
func (c *muxBwController) emitLifecycle(event string, rs *muxBwRouteState, gate string, elapsedNs int64) {
	c.emit(&MuxBandwidthEvent_LegLifecycle{LegLifecycle: &MuxLegLifecycle{
		Event:          event,
		RouteIndex:     int32(rs.index), //nolint:gosec // route index fits int32
		GateState:      gate,
		IntermediatePk: rs.intermediatePK,
		TransportKind:  rs.transportKind,
		ElapsedNs:      elapsedNs,
	}})
}

// totals sums the bytes pumped by EVERY leg the run ever had (including legs
// later dropped — their traffic really happened). Read after controlLoop exits.
func (c *muxBwController) totals() (sent, recv uint64) {
	for _, r := range c.all {
		sent += r.bytesSent.Load()
		recv += r.bytesRecv.Load()
	}
	return sent, recv
}

// peaks returns the peak instant send/recv rates the sampler observed.
func (c *muxBwController) peaks() (send, recv float64) {
	if c.tracker == nil {
		return 0, 0
	}
	return c.tracker.peakSend, c.tracker.peakRecv
}
