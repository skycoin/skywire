//go:build js && wasm

// Package main cmd/wasm-visor/proxyinstance_js.go c3-vis-wasm
// proxyinstance_js.go — a registry that models the tab's skysocks-client-lite
// clearnet proxy as a configurable, app-like instance (Phase 1 of the
// skysocks-lite-as-app design: project_skysocks_lite_proxy_control).
//
// Today (pre-registry) the exit PK was passed in on every fetchClearnet call and
// each browser window kept its own session (skysocks_js.go). Phase 1 introduces a
// single DEFAULT instance ("skysocks-client") that, after transports come up,
// auto-selects a RANDOM public proxy exit — so the iframe browser and the wallet
// "just work" without the operator hand-entering an exit — and surfaces it in the
// shared Apps tab (apps_js.go) where the exit is changeable. Multiple named
// instances + per-consumer binding + the ⚙-panel picker are Phases 2-3.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// defaultProxyID is the id/name of the built-in proxy instance. It matches the
// native launcher app name so the shared Angular Apps tab shows it identically.
const defaultProxyID = "skysocks-client"

// proxyInstance is one configurable skysocks-client-lite instance.
type proxyInstance struct {
	ID     string        `json:"name"`   // "skysocks-client"[-N]; native app name
	Label  string        `json:"label"`  // user-facing description
	ExitPK cipher.PubKey `json:"-"`      // current exit; zero = unset
	Exit   string        `json:"exit"`   // ExitPK.Hex() (or "") for JSON/JS
	Auto   bool          `json:"auto"`   // true = auto-select a random exit
	Status int           `json:"status"` // 0 stopped/configured, 1 running
	Detail string        `json:"detail"`
}

var (
	proxyRegMu sync.Mutex
	proxyReg   = map[string]*proxyInstance{
		defaultProxyID: {ID: defaultProxyID, Label: "Default (auto)", Auto: true},
	}
)

// Warm-pool tuning. Rather than pick ONE random exit and hope it routes, the
// default instance races several candidates from service discovery, keeps the
// ones that actually establish a route ("vetted"), makes the first the active
// exit, and holds the rest as pre-vetted standbys. If the active exit's route
// later dies mid-use, a standby is promoted immediately (no re-probe) and the
// pool is topped up in the background. This is what makes the wallet / iframe
// browser "just work" AND survive a dead first pick.
const (
	proxyRaceN      = 3 // candidates probed concurrently each selection round
	proxyPoolTarget = 2 // vetted exits kept: 1 active + up to 1 hot standby
)

var (
	proxyPoolMu      sync.Mutex
	proxyPool        []cipher.PubKey // vetted healthy exits; [0] is the active one
	proxyAutoRunning bool            // a selection loop is in flight (single-flight)
	proxyAutoCtx     context.Context // captured so failover can re-select
	proxyAutoSDPK    cipher.PubKey
)

// proxyInstanceExit returns the configured exit for an instance id.
func proxyInstanceExit(id string) (cipher.PubKey, bool) {
	proxyRegMu.Lock()
	defer proxyRegMu.Unlock()
	inst, ok := proxyReg[id]
	if !ok || inst.ExitPK.Null() {
		return cipher.PubKey{}, false
	}
	return inst.ExitPK, true
}

// proxyDefaultExit returns the default instance's exit (used by consumers that
// don't pin their own exit, so the auto-selected random proxy is the fallback).
func proxyDefaultExit() (cipher.PubKey, bool) { return proxyInstanceExit(defaultProxyID) }

// setProxyExit sets an instance's exit PK (the wasm analog of the native
// SetAppPK). Clearing (null pk) is allowed. Turns off Auto once the operator
// pins an exit explicitly.
func setProxyExit(id string, pk cipher.PubKey) error {
	proxyRegMu.Lock()
	defer proxyRegMu.Unlock()
	inst, ok := proxyReg[id]
	if !ok {
		return fmt.Errorf("unknown proxy instance: %s", id)
	}
	inst.ExitPK = pk
	inst.Exit = ""
	if !pk.Null() {
		inst.Exit = pk.Hex()
		inst.Auto = false // an explicit exit overrides auto-selection
	}
	return nil
}

// proxyInstanceRunning reports whether a live skysocks session exists to the
// instance's exit (any window). Used to derive the Apps-tab status.
func proxyInstanceRunning(pk cipher.PubKey) bool {
	if pk.Null() {
		return false
	}
	suffix := "|" + pk.Hex()
	skysocksMu.Lock()
	defer skysocksMu.Unlock()
	for k, s := range skysocksSessions {
		if len(k) >= len(suffix) && k[len(k)-len(suffix):] == suffix && s != nil && !s.IsClosed() {
			return true
		}
	}
	return false
}

// proxyInstancesSnapshot returns a copy of the registry with live status filled
// in, for the Apps tab (selfAppStates) and the JS listing hook.
func proxyInstancesSnapshot() []proxyInstance {
	proxyRegMu.Lock()
	out := make([]proxyInstance, 0, len(proxyReg))
	known := map[string]bool{}
	for _, inst := range proxyReg {
		cp := *inst
		if !cp.ExitPK.Null() {
			known[cp.ExitPK.Hex()] = true
		}
		out = append(out, cp)
	}
	proxyRegMu.Unlock()
	// Surface the vetted standby exits (pool[1:]) as selectable instances so the
	// iframe-browser / wallet proxy pickers can bind to an already-running
	// skysocks-client-lite rather than only "auto" or a hand-typed PK.
	proxyPoolMu.Lock()
	standbys := append([]cipher.PubKey(nil), proxyPool...)
	proxyPoolMu.Unlock()
	for i, pk := range standbys {
		if i == 0 || pk.Null() || known[pk.Hex()] {
			continue // [0] is the active default (already listed); skip dups
		}
		known[pk.Hex()] = true
		out = append(out, proxyInstance{
			ID:     defaultProxyID + "-standby-" + pk.Hex()[:8],
			Label:  "Standby (auto)",
			ExitPK: pk,
			Exit:   pk.Hex(),
			Auto:   true,
		})
	}
	for i := range out {
		if proxyInstanceRunning(out[i].ExitPK) {
			out[i].Status, out[i].Detail = 1, "Running — exit "+exitShort(out[i].ExitPK)
		} else if !out[i].ExitPK.Null() {
			out[i].Status, out[i].Detail = 0, "Ready — exit "+exitShort(out[i].ExitPK)
		} else {
			out[i].Status, out[i].Detail = 0, "Stopped (no exit selected)"
		}
	}
	return out
}

// isProxyInstance reports whether id names a registered proxy instance.
func isProxyInstance(id string) bool {
	proxyRegMu.Lock()
	defer proxyRegMu.Unlock()
	_, ok := proxyReg[id]
	return ok
}

// closeSkysocksExit closes every session to the given exit PK (any window /
// instance), so "stop" on a proxy instance actually severs its egress.
func closeSkysocksExit(pk cipher.PubKey) int {
	if pk.Null() {
		return 0
	}
	suffix := "|" + pk.Hex()
	skysocksMu.Lock()
	defer skysocksMu.Unlock()
	n := 0
	for k, s := range skysocksSessions {
		if len(k) >= len(suffix) && k[len(k)-len(suffix):] == suffix {
			if s != nil {
				_ = s.Close() //nolint:errcheck
			}
			delete(skysocksSessions, k)
			n++
		}
	}
	return n
}

// exitShort renders an exit PK for log/status ("none" when unset), reusing the
// package-wide shortPK(string) helper for the hex-truncation.
func exitShort(pk cipher.PubKey) string {
	if pk.Null() {
		return "none"
	}
	return shortPK(pk.Hex())
}

// reArmDefaultAuto re-flags the default instance as Auto after setProxyExit
// (which clears Auto on any non-null set) — an auto-selected exit should stay
// "auto" so only a later MANUAL change pins it.
func reArmDefaultAuto() {
	proxyRegMu.Lock()
	if p := proxyReg[defaultProxyID]; p != nil {
		p.Auto = true
	}
	proxyRegMu.Unlock()
}

// startDefaultProxyAuto waits for transports to come up, then fills the vetted
// proxy pool (see selectProxyPool) so the default instance has a live, routable
// exit — plus a standby for instant failover. Single-flight: a second call while
// one is running (e.g. failover re-select) is a no-op. Returns once the pool has
// an active exit; failover re-invokes it when the pool empties.
func startDefaultProxyAuto(ctx context.Context, sdPK cipher.PubKey) {
	proxyPoolMu.Lock()
	if proxyAutoRunning {
		proxyPoolMu.Unlock()
		return
	}
	proxyAutoRunning = true
	proxyAutoCtx, proxyAutoSDPK = ctx, sdPK
	proxyPoolMu.Unlock()
	defer func() {
		proxyPoolMu.Lock()
		proxyAutoRunning = false
		proxyPoolMu.Unlock()
	}()

	settle := time.Now().Add(8 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		// Stop if the operator pinned an exit (Auto off), or the pool already
		// has a live active exit.
		proxyRegMu.Lock()
		inst := proxyReg[defaultProxyID]
		pinned := inst != nil && !inst.Auto && !inst.ExitPK.Null()
		proxyRegMu.Unlock()
		if pinned {
			return
		}
		proxyPoolMu.Lock()
		haveActive := len(proxyPool) > 0
		proxyPoolMu.Unlock()
		if haveActive {
			return
		}
		// Wait for at least one transport (settle window) before probing.
		if tpM == nil || tpM.TransportCount() < 1 {
			if time.Now().Before(settle) {
				continue
			}
		}
		if selectProxyPool(ctx, sdPK, cipher.PubKey{}) {
			return
		}
		// SD empty / nothing routable yet — retry.
	}
}

// selectProxyPool races up to proxyRaceN random candidate exits from service
// discovery, probing each concurrently (a real route establishment — the only
// honest reachability test). The FIRST that routes becomes the active default
// exit immediately (fast start); later successes fill standby slots up to
// proxyPoolTarget. `avoid` is skipped (the just-failed exit on a re-select).
// Returns true if at least one exit was installed.
func selectProxyPool(ctx context.Context, sdPK cipher.PubKey, avoid cipher.PubKey) bool {
	cands := pickRandomProxyExits(ctx, sdPK, proxyRaceN, avoid)
	if len(cands) == 0 {
		return false
	}
	type probeRes struct {
		pk cipher.PubKey
		ok bool
	}
	resc := make(chan probeRes, len(cands))
	for _, pk := range cands {
		go func(pk cipher.PubKey) {
			resc <- probeRes{pk, probeExit(pk)}
		}(pk)
	}
	installed := false
	for i := 0; i < len(cands); i++ {
		r := <-resc
		if !r.ok {
			continue
		}
		if !installed {
			installed = true
			proxyPoolMu.Lock()
			proxyPool = []cipher.PubKey{r.pk}
			proxyPoolMu.Unlock()
			_ = setProxyExit(defaultProxyID, r.pk) //nolint:errcheck
			reArmDefaultAuto()
			vlog(fmt.Sprintf("[skysocks-lite] active proxy exit: %s", exitShort(r.pk)))
			continue
		}
		proxyPoolMu.Lock()
		if len(proxyPool) < proxyPoolTarget {
			proxyPool = append(proxyPool, r.pk)
			vlog(fmt.Sprintf("[skysocks-lite] standby proxy exit ready: %s", exitShort(r.pk)))
		}
		proxyPoolMu.Unlock()
	}
	return installed
}

// probeExit confirms an exit is actually routable by establishing a skysocks
// route to it (the 18s fast-fail dial in skysocksSession), then releasing the
// probe route. Returns true only if the route + session came up.
func probeExit(pk cipher.PubKey) bool {
	if pk.Null() || pk == selfPK {
		return false
	}
	win := "pool-probe-" + pk.Hex()[:8]
	if _, err := skysocksSession(win, pk); err != nil {
		return false
	}
	closeSkysocksWindow(win) // we only needed to confirm routability
	return true
}

// reportProxyExitDead is called when a route/dial to an exit fails. If it is the
// default instance's current AUTO-selected active exit, that exit is evicted, its
// live sessions are severed, and the next vetted standby is promoted instantly;
// if the pool empties, a fresh selection round is kicked off in the background.
// No-op for operator-pinned exits or any non-active exit (so probe failures and
// unrelated windows don't disturb the pool).
func reportProxyExitDead(pk cipher.PubKey) {
	if pk.Null() {
		return
	}
	proxyRegMu.Lock()
	inst := proxyReg[defaultProxyID]
	isActiveAuto := inst != nil && inst.Auto && inst.ExitPK == pk
	proxyRegMu.Unlock()
	if !isActiveAuto {
		return
	}
	proxyPoolMu.Lock()
	kept := proxyPool[:0]
	for _, e := range proxyPool {
		if e != pk {
			kept = append(kept, e)
		}
	}
	proxyPool = kept
	var next cipher.PubKey
	if len(proxyPool) > 0 {
		next = proxyPool[0]
	}
	ctx, sdPK := proxyAutoCtx, proxyAutoSDPK
	proxyPoolMu.Unlock()

	closeSkysocksExit(pk) // sever any live sessions to the dead exit
	if !next.Null() {
		_ = setProxyExit(defaultProxyID, next) //nolint:errcheck
		reArmDefaultAuto()
		vlog(fmt.Sprintf("[skysocks-lite] exit %s failed — promoted standby %s", exitShort(pk), exitShort(next)))
		// Top the pool back up to target in the background.
		if ctx != nil {
			go func() { selectProxyPool(ctx, sdPK, pk) }()
		}
		return
	}
	// Pool empty — clear the exit and reselect from scratch.
	_ = setProxyExit(defaultProxyID, cipher.PubKey{}) //nolint:errcheck
	reArmDefaultAuto()
	vlog(fmt.Sprintf("[skysocks-lite] exit %s failed — no standby, reselecting", exitShort(pk)))
	if ctx != nil {
		go startDefaultProxyAuto(ctx, sdPK)
	}
}

// pickRandomProxyExits fetches the public proxy list from service discovery over
// dmsg and returns up to n pseudo-randomly chosen exit PKs (self / null / avoid
// / duplicates excluded). No math/rand import (TinyGo-friendly) — a time-derived
// start offset spreads the choice across the list and across tabs.
func pickRandomProxyExits(ctx context.Context, sdPK cipher.PubKey, n int, avoid cipher.PubKey) []cipher.PubKey {
	if dmsgC == nil || n <= 0 {
		return nil
	}
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	status, _, body, err := dmsgclient.FetchOverDmsg(fctx, dmsgC, "GET", sdPK.Hex(), "/api/services?type=proxy", nil, nil)
	cancel()
	if err != nil || status != 200 {
		return nil
	}
	var services []servicedisc.Service
	if err := json.Unmarshal(body, &services); err != nil || len(services) == 0 {
		return nil
	}
	off := int(time.Now().UnixNano()) % len(services)
	if off < 0 {
		off += len(services)
	}
	var cand []cipher.PubKey
	for i := 0; i < len(services) && len(cand) < n; i++ {
		pk := services[(off+i)%len(services)].Addr.PubKey()
		if pk.Null() || pk == selfPK || pk == avoid {
			continue
		}
		dup := false
		for _, c := range cand {
			if c == pk {
				dup = true
				break
			}
		}
		if !dup {
			cand = append(cand, pk)
		}
	}
	return cand
}

// jsProxyInstances() → JSON array of the proxy instances (name/label/exit/auto/
// status/detail) for the ⚙ picker + Apps tab + the ip-check links.
func jsProxyInstances(_ js.Value, _ []js.Value) interface{} {
	b, err := json.Marshal(proxyInstancesSnapshot())
	if err != nil {
		return "[]"
	}
	return string(b)
}

// jsSetProxyExit(id, pkHex) sets an instance's exit ("" clears it). Returns an
// error string on failure, else null.
func jsSetProxyExit(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 || args[0].String() == "" {
		return "setProxyExit(id, pkHex)"
	}
	id := args[0].String()
	var pk cipher.PubKey
	if len(args) > 1 && !args[1].IsNull() && !args[1].IsUndefined() && args[1].String() != "" {
		if err := pk.UnmarshalText([]byte(args[1].String())); err != nil {
			return "bad pk: " + err.Error()
		}
	}
	if err := setProxyExit(id, pk); err != nil {
		return err.Error()
	}
	return nil
}
