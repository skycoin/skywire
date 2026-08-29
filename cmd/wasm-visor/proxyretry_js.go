//go:build js && wasm

// Package main cmd/wasm-visor/proxyretry_js.go c3-vis-wasm
// proxyretry_js.go — resilience for the default skysocks-client-lite instance:
//
//  1. An "ever connected" bit per exit so a dropped route is handled the way the
//     operator expects: retry the SAME key (exponential backoff) when the exit had
//     actually worked, but rotate to a NEW random exit when the very first
//     connection could never be established.
//  2. A proactive keepalive / liveness loop that yamux-pings the active exit (and
//     keeps the standbys warm) so a silently-dead route triggers the policy instead
//     of only being noticed on the next fetch — mirroring the native client's
//     sessionKeepAliveLoop.
//
// Companion to proxyinstance_js.go (which owns the pool + rotateAwayFromExit).
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Retry / liveness tuning.
const (
	stickyMaxTries          = 5                // same-key reconnect attempts before rotating
	stickyBaseDelay         = 1 * time.Second  // first backoff
	stickyMaxDelay          = 30 * time.Second // backoff cap
	proxyKeepalivePeriod    = 15 * time.Second // liveness ping cadence (mirrors native)
	proxyKeepaliveFailLimit = 2                // consecutive ping failures ⇒ exit declared dead
	// proxyFreezeGap is the since-last-sweep threshold beyond which a wake is
	// attributed to the SharedWorker having been FROZEN (timers suspended) rather
	// than to a normal — even heavily throttled — tick. The keepalive heartbeat
	// updates proxyLastSweepMs every proxyKeepalivePeriod (15s) while the worker
	// runs; background tab-throttling can stretch a live timer only to ~1 min, so
	// 3× the period (45s) since the last sweep means the worker was actually
	// suspended. Only then do we surface the "resuming" UI notice — a normal
	// short hide (worker still ticking) keeps the gap small and stays silent.
	proxyFreezeGap = 3 * proxyKeepalivePeriod
)

var (
	proxyConnMu        sync.Mutex
	proxyEverConnected = map[cipher.PubKey]bool{} // exits that established a real (non-probe) route
	proxyStickyActive  = map[cipher.PubKey]bool{} // a sticky-reconnect loop owns this exit
)

// isProbeProxyWindow reports the SELECTION probe window (pool-probe-*). A probe
// success only proves momentary routability for pool selection; it must NOT count
// as "ever connected" — otherwise the very first real fetch failure would wrongly
// be treated as a drop-of-a-working-exit instead of an initial-connect failure.
func isProbeProxyWindow(winID string) bool { return strings.HasPrefix(winID, "pool-probe-") }

// isInternalProxyWindow reports the visor's OWN skysocks windows (probe / sticky
// reconnect / keepalive / warm-standby). Their dial failures must NOT auto-trigger
// reportProxyExitDead — each manages its own failure handling — otherwise a
// keepalive/sticky failure would re-enter the policy it is already running.
func isInternalProxyWindow(winID string) bool {
	return strings.HasPrefix(winID, "pool-probe-") ||
		strings.HasPrefix(winID, "sticky-") ||
		strings.HasPrefix(winID, "keepalive-") ||
		strings.HasPrefix(winID, "pool-warm-")
}

// markProxyExitConnected records that a real (non-probe) route to pk came up.
func markProxyExitConnected(pk cipher.PubKey) {
	if pk.Null() {
		return
	}
	proxyConnMu.Lock()
	proxyEverConnected[pk] = true
	proxyConnMu.Unlock()
}

// isActiveAutoExit reports whether pk is the default instance's current
// auto-selected active exit (so pinned / other exits are left alone).
func isActiveAutoExit(pk cipher.PubKey) bool {
	proxyRegMu.Lock()
	defer proxyRegMu.Unlock()
	inst := proxyReg[defaultProxyID]
	return inst != nil && inst.Auto && inst.ExitPK == pk
}

// proxyRetryCtx returns the captured auto-selection context (falling back to the
// package ctx) so retry / keepalive loops stop with the visor.
func proxyRetryCtx() context.Context {
	proxyPoolMu.Lock()
	c := proxyAutoCtx
	proxyPoolMu.Unlock()
	if c == nil {
		return ctx
	}
	return c
}

// stickyReconnect retries the SAME exit with exponential backoff (capped) after an
// established exit dropped. On recovery it returns, leaving pk the active exit
// (real fetches re-dial their own routes); after stickyMaxTries it gives up and
// rotates to a standby / new random exit. Single-flight per pk.
func stickyReconnect(pk cipher.PubKey) {
	proxyConnMu.Lock()
	if proxyStickyActive[pk] {
		proxyConnMu.Unlock()
		return
	}
	proxyStickyActive[pk] = true
	proxyConnMu.Unlock()
	defer func() {
		proxyConnMu.Lock()
		delete(proxyStickyActive, pk)
		proxyConnMu.Unlock()
	}()

	actx := proxyRetryCtx()
	win := "sticky-" + pk.Hex()[:8]
	delay := stickyBaseDelay
	for i := 0; i < stickyMaxTries; i++ {
		select {
		case <-actx.Done():
			return
		case <-time.After(delay):
		}
		if !isActiveAutoExit(pk) {
			return // pinned to another exit, or already rotated away
		}
		if _, err := skysocksSession(win, pk); err == nil {
			closeSkysocksWindow(win) // release the probe route; real fetches re-dial
			vlog(fmt.Sprintf("[skysocks-lite] exit %s recovered on retry %d — staying", exitShort(pk), i+1))
			return
		}
		if delay *= 2; delay > stickyMaxDelay {
			delay = stickyMaxDelay
		}
	}
	vlog(fmt.Sprintf("[skysocks-lite] exit %s did not recover after %d tries — rotating", exitShort(pk), stickyMaxTries))
	rotateAwayFromExit(pk)
}

// proxyKeepaliveLoop periodically yamux-pings the active default exit so a
// silently-dead route is detected proactively and routed through reportProxyExitDead
// (which, because the exit had connected, retries the same key). It also keeps the
// standby exits warm — a live route held open — so a post-sticky rotation lands on
// a known-good exit. Started once from main after boot.
func proxyKeepaliveLoop(kctx context.Context) {
	fails := map[cipher.PubKey]int{}
	for {
		select {
		case <-kctx.Done():
			return
		case <-time.After(proxyKeepalivePeriod):
		case <-proxyWakeChan():
			// A page came back to the foreground (JS resume / visibilitychange →
			// visible). While the SharedWorker was frozen every setTimeout — and so
			// every Go WASM timer, including our proxyKeepalivePeriod one — was
			// suspended, so the active AND standby routes went silently dead
			// (yamux/dmsg keepalive i/o deadline on the far side) with no sweep to
			// notice. Do the sweep NOW instead of waiting out the (possibly
			// intensively-throttled, up to ~1 min) next tick, so recovery starts the
			// instant the tab is looked at rather than after a user-visible stall.
		}
		proxyResilienceSweep(fails)
	}
}

// proxyResilienceSweep performs one liveness pass: ping the active exit (dead ⇒
// policy), keep the standby routes warm, and keep the SHARED user-facing sessions
// hot. Shared by the periodic keepalive loop and the wake hook. `fails` carries
// consecutive-failure counts across calls so a transient miss doesn't evict.
func proxyResilienceSweep(fails map[cipher.PubKey]int) {
	// Freeze detection: this sweep's OWN lateness is the signal. proxyLastSweepMs is
	// stamped at the END of every sweep, so if more than proxyFreezeGap has elapsed
	// since the last one, the loop's timer (a JS setTimeout) was suspended — the
	// SharedWorker was frozen in the background — not merely throttled. Surface the
	// "resuming" notice. Whichever trigger runs the FIRST post-thaw sweep (the wake
	// nudge from visibilitychange, or the suspended periodic timer that also fires
	// immediately on thaw) detects it: because the stamp lands at the sweep's end,
	// both read the stale timestamp, so there is no race between them — a subtlety
	// found in runtime testing, where the periodic timer otherwise re-stamped a
	// fresh time before the wake path could read the stale one.
	if prev := proxyLastSweepMs.Load(); prev != 0 {
		if gap := time.Now().UnixMilli() - prev; gap > proxyFreezeGap.Milliseconds() &&
			proxyFreezeRecovering.CompareAndSwap(false, true) {
			proxyResumeNotice(true, gap)
		}
	}
	// Active exit: ping (establishing a keepalive session if needed).
	if pk, ok := proxyDefaultExit(); ok {
		if pingProxyExit("keepalive-"+pk.Hex()[:8], pk) {
			fails[pk] = 0
		} else {
			fails[pk]++
			if fails[pk] >= proxyKeepaliveFailLimit {
				delete(fails, pk)
				closeSkysocksWindow("keepalive-" + pk.Hex()[:8])
				reportProxyExitDead(pk) // policy: sticky (was connected) or rotate
			}
		}
	}
	// Standbys (pool[1:]): keep a warm route so promotion is instant + vetted.
	proxyPoolMu.Lock()
	standbys := append([]cipher.PubKey(nil), proxyPool...)
	proxyPoolMu.Unlock()
	for i, spk := range standbys {
		if i == 0 || spk.Null() {
			continue
		}
		pingProxyExit("pool-warm-"+spk.Hex()[:8], spk) // best-effort warmth
	}
	// Keep the SHARED user-facing sessions (owner = defaultProxyID, the key a
	// browser fetch resolves to) HOT for every pool exit — active AND standbys.
	// The pings above use throwaway windows for liveness POLICY; those do NOT
	// keep the route a real fetch reuses alive, so without this the shared route
	// goes cold and each navigation (and every promotion) cold-dials a fresh
	// multihop route (~30-45s) instead of reusing a live one — the gap vs a
	// native skysocks-client that holds one persistent route. Ping the live
	// session to keep it warm; re-establish (best-effort) any that dropped.
	for _, spk := range standbys {
		if spk.Null() {
			continue
		}
		skysocksMu.Lock()
		s, ok := skysocksSessions[skysocksKey(defaultProxyID, spk)]
		alive := ok && s != nil && !s.IsClosed()
		skysocksMu.Unlock()
		if alive {
			sess := s
			go func() { _, _ = sess.Ping() }() //nolint:errcheck // keep the shared route warm
		} else {
			prewarmDefaultSession(spk) // re-open the shared route in the background
		}
	}
	// Pool empty (both active and standby died during a freeze, or never filled)?
	// Kick a fresh selection so recovery doesn't wait for the next auto round.
	proxyPoolMu.Lock()
	empty := len(proxyPool) == 0
	proxyPoolMu.Unlock()
	if empty {
		kickDefaultProxyReselect()
	}
	// Stamp the liveness heartbeat: a wake compares now against this to tell a real
	// worker suspension (stale stamp) from a normal throttled tick (fresh stamp).
	proxyLastSweepMs.Store(time.Now().UnixMilli())
	// If a freeze-recovery notice is showing, clear it the moment the proxy is
	// usable again — an active exit with a live shared session — so the operator
	// sees "resuming" only for as long as recovery actually takes.
	if proxyFreezeRecovering.Load() && proxyPoolWarm() && proxyFreezeRecovering.CompareAndSwap(true, false) {
		proxyResumeNotice(false, 0)
	}
}

// proxyPoolWarm reports whether the default instance's active exit has a live
// shared user-facing session — i.e. a browser fetch would reuse a warm route
// rather than cold-dial. Used to decide when the freeze-recovery notice clears.
func proxyPoolWarm() bool {
	pk, ok := proxyDefaultExit()
	if !ok || pk.Null() {
		return false
	}
	skysocksMu.Lock()
	s, ok := skysocksSessions[skysocksKey(defaultProxyID, pk)]
	warm := ok && s != nil && !s.IsClosed()
	skysocksMu.Unlock()
	return warm
}

// Wake / freeze-detection state. The sweep runs on exactly one goroutine (the
// keepalive loop), and a wake is delivered as another case in that loop's SAME
// select — so a wake-sweep and the periodic-tick-sweep can never run
// concurrently, and `fails` plus these atomics are only ever touched from that
// one goroutine (jsProxyWake merely nudges the channel). The atomics are used so
// the values are safe to publish, not because there is contended access.
var (
	// proxyWakeCh is signalled (non-blocking) by jsProxyWake when a page reports it
	// returned to the foreground, so the keepalive loop runs a sweep immediately
	// instead of waiting for its (throttled-while-hidden) timer. Buffered depth 1:
	// coalesced wakes are fine — one sweep covers them.
	proxyWakeCh = make(chan struct{}, 1)
	// proxyLastSweepMs is the wall-clock (UnixMilli) of the last completed sweep —
	// the liveness heartbeat. 0 until the first sweep, so an early wake (e.g. the
	// initial visibilitychange at page load) is never mistaken for a thaw.
	proxyLastSweepMs atomic.Int64
	// proxyFreezeRecovering latches while the "resuming from background" notice is
	// shown, so repeated visibilitychange events don't re-emit it and the sweep
	// clears it exactly once.
	proxyFreezeRecovering atomic.Bool
)

func proxyWakeChan() <-chan struct{} { return proxyWakeCh }

// jsProxyWake() is called from the page's Page-Lifecycle `resume` /
// `visibilitychange`→visible handler (hv-boot.js) when the tab is looked at
// again. A backgrounded/frozen SharedWorker suspends every timer, so the
// keepalive loop cannot notice that the active + standby routes died during the
// idle period; this nudges it to sweep the moment the operator returns, turning a
// user-visible cold start into a background re-warm. Idempotent + cheap +
// non-blocking (a plain channel nudge; the sweep — and the freeze detection that
// drives the "resuming" UI notice — run on the loop, in proxyResilienceSweep).
func jsProxyWake(_ js.Value, _ []js.Value) interface{} {
	select {
	case proxyWakeCh <- struct{}{}:
	default: // a wake is already pending; the pending sweep covers this one
	}
	return nil
}

// proxyResumeNotice surfaces (recovering=true) or clears (false) the page's
// "resuming from background" toast via the worker→tab bridge in worker.js, and
// logs the transition. gapMs is the observed suspension length (ms), used only in
// the log line. Best-effort: absent bridge ⇒ log only.
func proxyResumeNotice(recovering bool, gapMs int64) {
	if h := js.Global().Get("__skywireProxyResume"); h.Type() == js.TypeFunction {
		h.Invoke(recovering, float64(gapMs))
	}
	if recovering {
		vlog(fmt.Sprintf("[skysocks-lite] worker resumed after ~%ds background suspension — re-establishing proxy", gapMs/1000))
	} else {
		vlog("[skysocks-lite] proxy re-warmed after background resume")
	}
}

// pingProxyExit ensures a session to pk under winID and yamux-pings it, returning
// true only if the ping round-tripped. Best-effort: any error is false.
func pingProxyExit(winID string, pk cipher.PubKey) bool {
	sess, err := skysocksSession(winID, pk)
	if err != nil {
		return false
	}
	if _, perr := sess.Ping(); perr != nil {
		return false
	}
	return true
}
